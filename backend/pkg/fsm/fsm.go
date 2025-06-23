package fsm

import (
	"errors"
	"fmt"

	"voip-client-backend/pkg/httpserver"
	"voip-client-backend/pkg/logger"

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
	WaitingInputs FSMState = iota + 1
	LaunchTTSAndWait
	WaitForDialCmdResponse
	WaitForCallEstablishment
	WaitForAusrcCmdResponse
	WaitForCallCompletion
)

func (s FSMState) String() string {
	switch s {
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

const logPrefix = "FSM"

type VoipClientFSM struct {
	logger        *logger.CustomLogger
	baresipHandle *gobaresip.Baresip
	currentState  FSMState
	// channels for communication with the Baresip instance

	numDialCmds            int
	pendingAudioFileToPlay string
	pendingCallCmdToken    string
	pendingAusrcCmdToken   string
}

func NewVoipClientFSM(logger *logger.CustomLogger, baresipHandle *gobaresip.Baresip) *VoipClientFSM {
	return &VoipClientFSM{
		currentState:  WaitingInputs,
		logger:        logger,
		baresipHandle: baresipHandle,
	}
}

func (fsm *VoipClientFSM) GetCurrentState() FSMState {
	return fsm.currentState
}

func (fsm *VoipClientFSM) transitionTo(state FSMState) {
	fsm.logger.InfoPkgf(logPrefix, "transitioning from state %s to %s", fsm.currentState.String(), state.String())
	fsm.currentState = state
}

func (fsm *VoipClientFSM) OnNewOutgoingCallRequest(newRequest httpserver.Payload) error {
	fsm.logger.InfoPkgf(logPrefix, "Received new outgoing call request: %+v", newRequest)

	if fsm.currentState != WaitingInputs {
		// FIXME: perhaps we might instead abort the current operation and start a new call?
		fsm.logger.Warnf("FSM is not in the WaitingInputs state, current state: %s. Ignoring new request.", fsm.currentState)
		return ErrInvalidState
	}

	// TODO: ask TTS to generate the WAV file

	// convert it using ffmpeg

	// fsm.transitionTo(LaunchCallAndWaitForEstabilishment)

	fsm.pendingAudioFileToPlay = "/usr/share/baresip/test-message.wav"

	// Dial a new call
	fsm.numDialCmds++
	fsm.pendingCallCmdToken = fmt.Sprintf("dial_cmd_%d", fsm.numDialCmds)
	err := fsm.baresipHandle.Cmd("dial", newRequest.CalledNumber, fsm.pendingCallCmdToken)
	if err != nil {
		fsm.logger.InfoPkgf(logPrefix, "Error dialing: %s", err)
		fsm.transitionTo(WaitingInputs)
	}

	fsm.transitionTo(WaitForDialCmdResponse)

	return nil
}

func (fsm *VoipClientFSM) OnBaresipCmdResponse(response gobaresip.ResponseMsg) error {
	fsm.logger.InfoPkgf(logPrefix, "Received baresip response with TOKEN: %s", response.Token)

	switch fsm.currentState {
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

	if fsm.currentState != WaitForCallCompletion {
		fsm.logger.Warnf("FSM is not in the WaitForCallCompletion state, current state: %s. Ignoring new request.", fsm.currentState)
		return ErrInvalidState
	}

	fsm.transitionTo(WaitingInputs)

	return nil
}
