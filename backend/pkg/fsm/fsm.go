package fsm

import (
	"fmt"
	"time"

	"voip-client-backend/pkg/logger"
	"voip-client-backend/pkg/tts"

	"github.com/dustin/go-broadcast"
	"github.com/f18m/go-baresip/pkg/gobaresip"
)

type FSMState int

const (
	Uninitialized FSMState = iota + 1
	WaitingUserAgentRegistration
	WaitingInputs
	WaitForCallEstablishment
	WaitForCallCompletion
)

func (s FSMState) String() string {
	switch s {
	case Uninitialized:
		return "Uninitialized"
	case WaitingUserAgentRegistration:
		return "WaitingUserAgentRegistration"
	case WaitingInputs:
		return "WaitingInputs"
	case WaitForCallEstablishment:
		return "WaitForCallEstablishment"
	case WaitForCallCompletion:
		return "WaitForCallCompletion"
	default:
		return fmt.Sprintf("Unknown FSMState(%d)", s)
	}
}

// NewCallRequest is the type to use to request a [VoipClientFSM] to start a new call.
type NewCallRequest struct {
	CalledNumber string
	MessageTTS   string
}

/*
VoipClientFSM is the Finite State Machine (FSM) that keeps track of the current state of the VoIP client.
Note that this type is not thread-safe, so all its methods must be invoked from a single goroutine.

Visit https://www.mermaidchart.com/play and paste the following code to visualize the state machine:

	flowchart TD

		Uninitialized("**Uninitialized**")
		WaitingUserAgentRegistration("**WaitingUserAgentRegistration**<br>Add SIP UA to Baresip, which starts registration/auth")
		WaitingInputs("**WaitingInputs**<br>Waiting for new call requests from HA")
		WaitForCallEstablishment("**WaitForCallEstablishment**<br>Run the TTS engine to produce a WAV file. Ask baresip to start the call, then wait")
		WaitForCallCompletion("**WaitForCallCompletion**<br>Ask baresip to reproduce the TTS message")

		Uninitialized -- "Baresip TCP socket connected" --> WaitingUserAgentRegistration
		WaitingUserAgentRegistration -- "Baresip Event: Register OK" --> WaitingInputs
		WaitingInputs -- "HTTP Call Request from HA" --> WaitForCallEstablishment
		WaitForCallEstablishment -- "Baresip call ESTABLISHED event" --> WaitForCallCompletion
		WaitForCallCompletion -- "Baresip call CLOSED event" --> WaitingInputs
		WaitForCallCompletion -- "Baresip End-of-File event (send hangup command)" --> WaitingInputs

	    WaitForCallEstablishment -- "Timeout during establishment" --> WaitingInputs
	    WaitForCallCompletion -- "Timeout during call" --> WaitingInputs
*/
type VoipClientFSM struct {
	// config
	maxVoiceCallDuration time.Duration

	// link to other objects
	logger        *logger.CustomLogger
	baresipHandle *gobaresip.Baresip
	ttsService    *tts.TTSService

	// state changes channel
	stateChangesPubCh broadcast.Broadcaster

	// main state machine state
	currentState FSMState

	// secondary state variables
	registered             bool
	numDialCmds            int
	pendingAudioFileToPlay string
	currentCallId          string
	currentCallStartTime   time.Time
}

/*
func panicIf(condition bool) {
	if condition {
		panic("state invariant condition not met, this is a bug -- please report it as Github issue at https://github.com/f18m/ha-addon-voip-client/issues")
	}
}
*/

func NewVoipClientFSM(
	logger *logger.CustomLogger,
	baresipHandle *gobaresip.Baresip,
	ttsService *tts.TTSService,
	fsmStatePubSub broadcast.Broadcaster,
	maxVoiceCallDuration time.Duration) *VoipClientFSM {
	return &VoipClientFSM{
		currentState:         Uninitialized, // initial state
		logger:               logger,
		baresipHandle:        baresipHandle,
		ttsService:           ttsService,
		maxVoiceCallDuration: maxVoiceCallDuration,
		stateChangesPubCh:    fsmStatePubSub,
	}
}

func (fsm *VoipClientFSM) GetCurrentState() FSMState {
	return fsm.currentState
}

func (fsm *VoipClientFSM) getLogPrefix() string {
	return fmt.Sprintf("fsm [%s]", fsm.currentState.String())
}

func (fsm *VoipClientFSM) transitionTo(state FSMState) {
	fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Transitioning from state %s to %s",
		fsm.currentState.String(), state.String())
	fsm.currentState = state

	// ensure invariants for each state are respected:
	if state == WaitingInputs {
		fsm.pendingAudioFileToPlay = ""
		fsm.currentCallId = ""
		fsm.currentCallStartTime = time.Time{} // empty time
	}

	// notify listeners, if any
	// NOTE: compared to a regular go channel, the broadcaster allows multiple subscribers
	//       and won't block if no one is listening
	fsm.stateChangesPubCh.Submit(fsm.currentState)
}

func (fsm *VoipClientFSM) InitializeUserAgent(sip_uri, password string) error {
	fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Initializing User Agent [%s]", sip_uri)

	if fsm.currentState != Uninitialized {
		fsm.logger.WarnPkgf(fsm.getLogPrefix(), "FSM is not in the Uninitialized state, current state: %s. Ignoring initialization request.", fsm.currentState)
		return ErrInvalidState
	}

	_, err := fsm.baresipHandle.CmdTxWithAck(gobaresip.CommandMsg{
		Command: "uanew",
		Params:  fmt.Sprintf("%s;auth_pass=%s", sip_uri, password),
	})
	if err != nil {
		fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Failed to create new SIP User Agent: %s", err)
		return err
	}
	fsm.transitionTo(WaitingUserAgentRegistration)
	return nil
}

/* -------------------------------------------------------------------------- */
/*                                TIMER EVENTS                                */
/* -------------------------------------------------------------------------- */

func (fsm *VoipClientFSM) OnTimeoutTicker() {

	switch fsm.currentState {
	case Uninitialized, WaitingUserAgentRegistration, WaitingInputs:
		// ignore timer... there is no timeout associated to these FSM states
		return

	case WaitForCallEstablishment, WaitForCallCompletion:
		fsm.logger.InfoPkgf(fsm.getLogPrefix(), "start call time is %s; max duration is %s", fsm.currentCallStartTime, fsm.maxVoiceCallDuration)

		if !fsm.currentCallStartTime.IsZero() &&
			time.Since(fsm.currentCallStartTime) > fsm.maxVoiceCallDuration {

			// if the current state is "WaitForCallEstablishment", then it means we
			// reached timeout for the whole call even before the call becomes established
			fsm.logger.WarnPkgf(fsm.getLogPrefix(), "Timeout after %s in state [%s]. Call [%s] aborted.",
				fsm.maxVoiceCallDuration.String(), fsm.currentState.String(), fsm.currentCallId)

			_, err := fsm.baresipHandle.CmdHangupID(fsm.currentCallId)
			if err != nil {
				fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Error hanging up the call after timeout: %s", err)

				// keep going
			}

			fsm.transitionTo(WaitingInputs)
		}
	}
}

/* -------------------------------------------------------------------------- */
/*                            HOMEASSISTANT EVENTS                            */
/* -------------------------------------------------------------------------- */

func (fsm *VoipClientFSM) OnNewOutgoingCallRequest(newRequest NewCallRequest) error {
	fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Received new outgoing call request: %+v", newRequest)

	if fsm.currentState != WaitingInputs {
		// FIXME: perhaps we might instead abort the current operation and start a new call?
		fsm.logger.WarnPkgf(fsm.getLogPrefix(), "FSM is not in the WaitingInputs state, current state: %s. Dropping the new call request. Please wait for previous call to get closed.", fsm.currentState)
		return ErrInvalidState
	}

	// ask TTS to generate the WAV file and get its path
	var err error
	fsm.pendingAudioFileToPlay, err = fsm.ttsService.GetAudioFile(newRequest.MessageTTS)
	if err != nil {
		fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Error doing the Text-to-Speech conversion: %s", err)
		fsm.transitionTo(WaitingInputs)
		return nil
	}

	// TODO1: detect if it's necessary to convert the audio file using ffmpeg
	// As of Aug 2025, Google Translate produces WAVs that Baresip can handle, so this is not
	// strictly necessary... but in future who knows?

	// TODO2: it would be good to check if the DURATION of the audio file is LONGER than
	// the 	fsm.maxVoiceCallDuration, and if so, warn the user that the call will be aborted
	// after fsm.maxVoiceCallDuration seconds, even if the audio file is not finished

	// Dial a new call
	fsm.numDialCmds++
	fsm.currentCallStartTime = time.Now()
	_, err2 := fsm.baresipHandle.CmdDial(newRequest.CalledNumber)
	if err2 != nil {
		fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Error dialing: %s", err2)
		fsm.transitionTo(WaitingInputs)
		return nil
	}
	fsm.transitionTo(WaitForCallEstablishment)

	fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Dial command sent successfully, waiting up to %s for call to be established...",
		fsm.maxVoiceCallDuration.String())

	return nil
}

/* -------------------------------------------------------------------------- */
/*                               BARESIP EVENTS                               */
/* -------------------------------------------------------------------------- */

func (fsm *VoipClientFSM) OnRegisterOk(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Successful SIP REGISTER for: %s. This is good news. It means your 'voip_provider' addon configuration is valid and Baresip authenticated against your VOIP provider. Now calls can be made and can be received!", event.AccountAOR)
	fsm.registered = true

	if fsm.currentState == WaitingUserAgentRegistration {
		fsm.transitionTo(WaitingInputs)
	}
	//else: Baresip (as every SIP UA) will periodically re-attempt registration (typically every 1h);
	//      when that happens this function gets invoked and it might even happen during an outgoing call;
	//      in such (unlikely) case, remain in whatever state the FSM already is

	return nil
}

func (fsm *VoipClientFSM) OnRegisterFail(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Failed SIP REGISTER for: %s. This typically means that the 'voip_provider' addon configuration is invalid (either user or password is invalid). Please check above logs for more details. The addon won't work until the configuration will be fixed.", event.AccountAOR)
	fsm.registered = false

	// in this state any communication will fail... go back to the initial state
	fsm.transitionTo(WaitingUserAgentRegistration)
	return nil
}

func (fsm *VoipClientFSM) OnCallOutgoing(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Received outgoing call notification for call ID (%s) and Peer URI: %s",
		event.ID, event.PeerURI)

	fsm.currentCallId = event.ID

	// No need to transition into any new state...
	// the call will progress autonomously either to CLOSE or ESTABLISHED statuses

	return nil
}

func (fsm *VoipClientFSM) OnCallEstablished(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Received call estabilished status update for Peer URI: %s", event.PeerURI)

	if fsm.currentState != WaitForCallEstablishment {
		fsm.logger.WarnPkgf(fsm.getLogPrefix(), "FSM is not in the WaitForCallEstablishment state, current state: %s. Ignoring new request.", fsm.currentState)
		return ErrInvalidState
	}

	if fsm.currentCallId != "" &&
		fsm.currentCallId != event.ID {
		fsm.logger.WarnPkgf(fsm.getLogPrefix(), "Received call established event for a different call ID (%s), expected %s. This is a bug.",
			event.ID, fsm.currentCallId)
		return ErrInvalidState
	}

	_, err := fsm.baresipHandle.CmdAusrc("aufile", fsm.pendingAudioFileToPlay)
	if err != nil {
		fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Error setting audio source to the right file: %s", err)
		fsm.transitionTo(WaitForCallCompletion)
		return nil
	}

	fsm.transitionTo(WaitForCallCompletion)

	// reset timeout counter:
	fsm.currentCallStartTime = time.Now()
	fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Audio playback was started successfully, waiting up to %s for the audio file to complete...",
		fsm.maxVoiceCallDuration.String())

	return nil
}

func (fsm *VoipClientFSM) OnEndOfFile(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Received end-of-file notification: %s", event.PeerURI)

	if fsm.currentState != WaitForCallCompletion {
		fsm.logger.WarnPkgf(fsm.getLogPrefix(), "FSM is not in the WaitForCallCompletion state, current state: %s. Ignoring new request.", fsm.currentState)
		return ErrInvalidState
	}

	// hang up the call!
	_, err := fsm.baresipHandle.CmdHangupID(fsm.currentCallId)
	if err != nil {
		fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Error hanging up the call: %s", err)
		return nil
	}

	return nil
}

func (fsm *VoipClientFSM) OnCallClosed(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Received call closed event for Peer URI: %s", event.PeerURI)

	if fsm.currentState == WaitingInputs {
		fsm.logger.WarnPkgf(fsm.getLogPrefix(), "FSM is not in a state where a call should be active, current state: %s. This is a bug.", fsm.currentState)
		return ErrInvalidState
	}

	if fsm.currentCallId != "" &&
		fsm.currentCallId != event.ID {
		fsm.logger.WarnPkgf(fsm.getLogPrefix(), "Received call closed event for a different call ID (%s), expected %s. This is a bug.",
			event.ID, fsm.currentCallId)
		return ErrInvalidState
	}

	fsm.logger.InfoPkgf(fsm.getLogPrefix(), "Aborting any operation in progress since the call %s has ended...", event.ID)
	fsm.transitionTo(WaitingInputs)

	return nil
}
