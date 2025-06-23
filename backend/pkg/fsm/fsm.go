package fsm

import (
	"errors"
	"fmt"

	"voip-client-backend/pkg/httpserver"
	"voip-client-backend/pkg/logger"
	"voip-client-backend/pkg/tts"

	"github.com/f18m/go-baresip/pkg/gobaresip"
)

/*
---
config:

	theme: redux

---
flowchart TD

	    WaitingInputs
		LaunchTTSAndWait
		WaitForDialCmdResponse
		WaitForCallEstablishment
		WaitForAusrcCmdResponse
		WaitForCallCompletion

	    WaitingInputs --HTTP Call Request --> WaitForDialCmdResponse
	    WaitForDialCmdResponse -- Baresip cmd response --> WaitForCallEstablishment
	    WaitForCallEstablishment -- Baresip call event --> WaitForAusrcCmdResponse
	    WaitForAusrcCmdResponse -- Baresip cmd response --> WaitForCallCompletion
	    WaitForCallCompletion -- Baresip call event --> WaitingInputs
*/

type FSMState int

const (
	Uninitialized FSMState = iota + 1
	WaitingUserAgentRegistration
	WaitingInputs
	LaunchTTSAndWait
	WaitForDialCmdResponse
	WaitForCallEstablishment
	WaitForAusrcCmdResponse
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
	case LaunchTTSAndWait:
		return "LaunchTTSAndWait"
	case WaitForDialCmdResponse:
		return "WaitForDialCmdResponse"
	case WaitForCallEstablishment:
		return "WaitForCallEstablishment"
	case WaitForAusrcCmdResponse:
		return "WaitForAusrcCmdResponse"
	case WaitForCallCompletion:
		return "WaitForCallCompletion"
	default:
		return fmt.Sprintf("Unknown FSMState(%d)", s)
	}
}

const logPrefix = "fsm"

type VoipClientFSM struct {
	logger        *logger.CustomLogger
	baresipHandle *gobaresip.Baresip
	ttsService    *tts.TTSService

	// main state machine state
	currentState FSMState

	// secondary state variables
	registered             bool
	numDialCmds            int
	pendingUaInitCmdToken  string
	pendingAudioFileToPlay string
	pendingCallCmdToken    string
	pendingAusrcCmdToken   string
	currentCallId          string
}

func panicIf(condition bool) {
	if condition {
		panic("state invariant condition not met, this is a bug -- please report it as Github issue at https://github.com/f18m/ha-addon-voip-client/issues")
	}
}

func NewVoipClientFSM(logger *logger.CustomLogger, baresipHandle *gobaresip.Baresip, ttsService *tts.TTSService) *VoipClientFSM {
	return &VoipClientFSM{
		currentState:  Uninitialized, // initial state
		logger:        logger,
		baresipHandle: baresipHandle,
		ttsService:    ttsService,
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
	switch state {
	case WaitingInputs:
		fsm.pendingAudioFileToPlay = ""
		fsm.pendingAusrcCmdToken = ""
		fsm.pendingCallCmdToken = ""
		fsm.currentCallId = ""

	case WaitForDialCmdResponse:
		panicIf(fsm.pendingCallCmdToken == "")
	}
}

func (fsm *VoipClientFSM) InitializeUserAgent(sip_uri, password string) error {
	fsm.logger.InfoPkgf(logPrefix, "Initializing User Agent [%s]", sip_uri)

	if fsm.currentState != Uninitialized {
		fsm.logger.Warnf("FSM is not in the Uninitialized state, current state: %s. Ignoring initialization request.", fsm.currentState)
		return ErrInvalidState
	}

	fsm.pendingUaInitCmdToken = "uaregistration_token"
	err := fsm.baresipHandle.Cmd("uanew", fmt.Sprintf("%s;auth_pass=%s", sip_uri, password), fsm.pendingUaInitCmdToken)
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
	fsm.transitionTo(WaitingInputs)
	return nil
}

func (fsm *VoipClientFSM) OnRegisterFail(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(logPrefix, "Failed SIP REGISTER for: %s. This typically means that the 'voip_provider' addon configuration is invalid (either user or password is invalid). Please check above logs for more details. The addon won't work until the configuration will be fixed.", event.AccountAOR)
	fsm.registered = false

	// in this state any communication will fail... go back to the initial state
	fsm.transitionTo(WaitingUserAgentRegistration)
	return nil
}

func (fsm *VoipClientFSM) OnNewOutgoingCallRequest(newRequest httpserver.Payload) error {
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
		fsm.logger.InfoPkgf(logPrefix, "Error doing the Text-to-Speech: %s", err)
		fsm.transitionTo(WaitingInputs)
		return nil
	}

	// TODO: convert it using ffmpeg
	// fsm.transitionTo(LaunchCallAndWaitForEstabilishment)

	// Dial a new call
	fsm.numDialCmds++
	fsm.pendingCallCmdToken = fmt.Sprintf("dial_cmd_%d", fsm.numDialCmds)
	err = fsm.baresipHandle.Cmd("dial", newRequest.CalledNumber, fsm.pendingCallCmdToken)
	if err != nil {
		fsm.logger.InfoPkgf(logPrefix, "Error dialing: %s", err)
		fsm.transitionTo(WaitingInputs)
		return nil
	}

	fsm.transitionTo(WaitForDialCmdResponse)
	return nil
}

func (fsm *VoipClientFSM) OnBaresipCmdResponse(response gobaresip.ResponseMsg) error {
	fsm.logger.InfoPkgf(logPrefix, "Received baresip response with TOKEN: %s", response.Token)

	switch fsm.currentState {
	case WaitingUserAgentRegistration:
		if response.Token != fsm.pendingUaInitCmdToken {
			fsm.logger.Warnf("Unexpected response token %s; was waiting for token %s", response.Token, fsm.pendingUaInitCmdToken)
			return errors.New("unexpected response token")
		}

		// no state transition... wait for the REGISTER OK or REGISTER FAIL events...

	case WaitForDialCmdResponse:
		if response.Token != fsm.pendingCallCmdToken {
			fsm.logger.Warnf("Unexpected response token %s; was waiting for token %s", response.Token, fsm.pendingCallCmdToken)
			return errors.New("unexpected response token")
		}

		if !response.Ok {
			fsm.logger.Warnf("Baresip failed to initiate the new call: %s. Going back into WaitingInputs", response.Data)
			fsm.transitionTo(WaitingInputs)
			return nil
		}

		fsm.transitionTo(WaitForCallEstablishment)

	case WaitForAusrcCmdResponse:
		if response.Token != fsm.pendingAusrcCmdToken {
			fsm.logger.Warnf("Unexpected response token %s; was waiting for token %s", response.Token, fsm.pendingAusrcCmdToken)
			return errors.New("unexpected response token")
		}

		if !response.Ok {
			fsm.logger.Warnf("Baresip failed to setup the right audio: %s...", response.Data)
			// fsm.transitionTo(WaitingInputs)
			// return nil
		}

		fsm.transitionTo(WaitForCallCompletion)

	default:
		fsm.logger.Warnf("FSM is not in a WaitForCmdResponse state, current state: %s. Ignoring new request.", fsm.currentState)
		return ErrInvalidState
	}

	return nil
}

func (fsm *VoipClientFSM) OnCallEstablished(event gobaresip.EventMsg) error {
	fsm.logger.InfoPkgf(logPrefix, "Received call estabilished status update for Peer URI: %s", event.PeerURI)

	if fsm.currentState != WaitForCallEstablishment {
		fsm.logger.Warnf("FSM is not in the WaitForCallEstablishment state, current state: %s. Ignoring new request.", fsm.currentState)
		return ErrInvalidState
	}

	fsm.pendingAusrcCmdToken = fmt.Sprintf("ausrc_cmd_%d", fsm.numDialCmds)
	err := fsm.baresipHandle.Cmd("ausrc", fmt.Sprintf("aufile,%s", fsm.pendingAudioFileToPlay), fsm.pendingAusrcCmdToken)
	if err != nil {
		fsm.logger.InfoPkgf(logPrefix, "Error setting audio source to the right file: %s", err)
		fsm.transitionTo(WaitForCallCompletion)
		return nil
	}

	fsm.transitionTo(WaitForAusrcCmdResponse)
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
