package fsm

import (
	"fmt"

	"voip-client-backend/pkg/logger"
	"voip-client-backend/pkg/tts"

	"github.com/dustin/go-broadcast"
	"github.com/f18m/go-baresip/pkg/gobaresip"
)

/*
Visit https://www.mermaidchart.com/play

flowchart TD
    Uninitialized("**Uninitialized**")
    WaitingUserAgentRegistration("**WaitingUserAgentRegistration**<br>Add SIP UA to Baresip, which starts registration/auth")
    WaitingInputs("**WaitingInputs**<br>Waiting for new call requests from HA")
    WaitForCallEstablishment("**WaitForCallEstablishment**<br>Run the TTS engine to produce a WAV file. Ask baresip to start the call, then wait")
    WaitForCallCompletion("**WaitForCallCompletion**<br>Ask baresip to reproduce the TTS message")

    Uninitialized -- Baresip TCP skt connected --> WaitingUserAgentRegistration
    WaitingUserAgentRegistration -- Baresip Event: Register OK --> WaitingInputs
    WaitingInputs --HTTP Call Request from HA --> WaitForCallEstablishment
    WaitForCallEstablishment -- Baresip call ESTABLISHED event --> WaitForCallCompletion
    WaitForCallCompletion -- Baresip call CLOSED event --> WaitingInputs
    WaitForCallCompletion -- Baresip End-of-File event (send hangup command) --> WaitingInputs

*/

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

const logPrefix = "fsm"

type NewCallRequest struct {
	CalledNumber string
	MessageTTS   string
}

type VoipClientFSM struct {
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
	fsmStatePubSub broadcast.Broadcaster) *VoipClientFSM {
	return &VoipClientFSM{
		currentState:      Uninitialized, // initial state
		logger:            logger,
		baresipHandle:     baresipHandle,
		ttsService:        ttsService,
		stateChangesPubCh: fsmStatePubSub,
	}
}

func (fsm *VoipClientFSM) GetCurrentState() FSMState {
	return fsm.currentState
}

func (fsm *VoipClientFSM) transitionTo(state FSMState) {
	fsm.logger.InfoPkgf(logPrefix, "Transitioning from state %s to %s",
		fsm.currentState.String(), state.String())
	fsm.currentState = state

	// ensure invariants for each state are respected:
	if state == WaitingInputs {
		fsm.pendingAudioFileToPlay = ""
		fsm.currentCallId = ""
	}

	// notify listeners, if any
	// NOTE: compared to a regular go channel, the broadcaster allows multiple subscribers
	//       and won't block if no one is listening
	fsm.stateChangesPubCh.Submit(fsm.currentState)
}

func (fsm *VoipClientFSM) InitializeUserAgent(sip_uri, password string) error {
	fsm.logger.InfoPkgf(logPrefix, "Initializing User Agent [%s]", sip_uri)

	if fsm.currentState != Uninitialized {
		fsm.logger.Warnf("FSM is not in the Uninitialized state, current state: %s. Ignoring initialization request.", fsm.currentState)
		return ErrInvalidState
	}

	_, err := fsm.baresipHandle.CmdTxWithAck(gobaresip.CommandMsg{
		Command: "uanew",
		Params:  fmt.Sprintf("%s;auth_pass=%s", sip_uri, password),
	})
	if err != nil {
		fsm.logger.InfoPkgf(logPrefix, "Failed to create new SIP User Agent: %s", err)
		return err
	}
	fsm.transitionTo(WaitingUserAgentRegistration)
	return nil
}

func (fsm *VoipClientFSM) OnRegisterOk(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(logPrefix, "Successful SIP REGISTER for: %s. This is good news. It means your 'voip_provider' addon configuration is valid and Baresip authenticated against your VOIP provider. Now calls can be made and can be received!", event.AccountAOR)
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
	fsm.logger.InfoPkgf(logPrefix, "Failed SIP REGISTER for: %s. This typically means that the 'voip_provider' addon configuration is invalid (either user or password is invalid). Please check above logs for more details. The addon won't work until the configuration will be fixed.", event.AccountAOR)
	fsm.registered = false

	// in this state any communication will fail... go back to the initial state
	fsm.transitionTo(WaitingUserAgentRegistration)
	return nil
}

func (fsm *VoipClientFSM) OnNewOutgoingCallRequest(newRequest NewCallRequest) error {
	fsm.logger.InfoPkgf(logPrefix, "Received new outgoing call request: %+v", newRequest)

	if fsm.currentState != WaitingInputs {
		// FIXME: perhaps we might instead abort the current operation and start a new call?
		fsm.logger.Warnf("FSM is not in the WaitingInputs state, current state: %s. Dropping the new call request. Please wait for previous call to get closed.", fsm.currentState)
		return ErrInvalidState
	}

	// ask TTS to generate the WAV file and get its path
	var err error
	fsm.pendingAudioFileToPlay, err = fsm.ttsService.GetAudioFile(newRequest.MessageTTS)
	if err != nil {
		fsm.logger.InfoPkgf(logPrefix, "Error doing the Text-to-Speech conversion: %s", err)
		fsm.transitionTo(WaitingInputs)
		return nil
	}

	// TODO: detect if it's necessary to convert the audio file using ffmpeg -- right now Google Translate produces WAVs that Baresip can handle

	// Dial a new call
	fsm.numDialCmds++
	_, err2 := fsm.baresipHandle.CmdTxWithAck(gobaresip.CommandMsg{
		Command: "dial",
		Params:  newRequest.CalledNumber,
	})
	if err2 != nil {
		fsm.logger.InfoPkgf(logPrefix, "Error dialing: %s", err2)
		fsm.transitionTo(WaitingInputs)
		return nil
	}
	fsm.transitionTo(WaitForCallEstablishment)
	return nil
}

func (fsm *VoipClientFSM) OnCallOutgoing(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(logPrefix, "Received call outgoing for Peer URI: %s", event.PeerURI)

	fsm.currentCallId = event.ID

	// No need to transition into any new state... the call will progress autonomously either to CLOSE or ESTABLISHED statuses

	return nil
}

func (fsm *VoipClientFSM) OnCallEstablished(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(logPrefix, "Received call estabilished status update for Peer URI: %s", event.PeerURI)

	if fsm.currentState != WaitForCallEstablishment {
		fsm.logger.Warnf("FSM is not in the WaitForCallEstablishment state, current state: %s. Ignoring new request.", fsm.currentState)
		return ErrInvalidState
	}

	if fsm.currentCallId != "" &&
		fsm.currentCallId != event.ID {
		fsm.logger.Warnf("Received call established event for a different call ID (%s), expected %s. This is a bug.",
			event.ID, fsm.currentCallId)
		return ErrInvalidState
	}

	_, err := fsm.baresipHandle.CmdTxWithAck(gobaresip.CommandMsg{
		Command: "ausrc",
		Params:  fmt.Sprintf("aufile,%s", fsm.pendingAudioFileToPlay),
	})
	if err != nil {
		fsm.logger.InfoPkgf(logPrefix, "Error setting audio source to the right file: %s", err)
		fsm.transitionTo(WaitForCallCompletion)
		return nil
	}

	fsm.transitionTo(WaitForCallCompletion)
	return nil
}

func (fsm *VoipClientFSM) OnEndOfFile(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(logPrefix, "Received end-of-file notification: %s", event.PeerURI)

	if fsm.currentState != WaitForCallCompletion {
		fsm.logger.Warnf("FSM is not in the WaitForCallCompletion state, current state: %s. Ignoring new request.", fsm.currentState)
		return ErrInvalidState
	}

	// hang up the call!
	_, err := fsm.baresipHandle.CmdHangup()
	if err != nil {
		fsm.logger.InfoPkgf(logPrefix, "Error hanging up the call: %s", err)
		return nil
	}

	return nil
}

func (fsm *VoipClientFSM) OnCallClosed(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(logPrefix, "Received call closed event for Peer URI: %s", event.PeerURI)

	if fsm.currentState == WaitingInputs {
		fsm.logger.Warnf("FSM is not in a state where a call should be active, current state: %s. This is a bug.", fsm.currentState)
		return ErrInvalidState
	}

	if fsm.currentCallId != "" &&
		fsm.currentCallId != event.ID {
		fsm.logger.Warnf("Received call closed event for a different call ID (%s), expected %s. This is a bug.",
			event.ID, fsm.currentCallId)
		return ErrInvalidState
	}

	fsm.logger.InfoPkgf(logPrefix, "Aborting any operation in progress since the call %s has ended...", event.ID)
	fsm.transitionTo(WaitingInputs)

	return nil
}
