package fsm

import (
	"errors"
	"fmt"

	"voip-client-backend/pkg/httpserver"
	"voip-client-backend/pkg/logger"

	"github.com/f18m/go-baresip/pkg/gobaresip"
)

const (
	WaitingInputs = iota
	LaunchTTSAndWait
	WaitForDialCmdResponse
	WaitForCallEstablishment
	WaitForAusrcCmdResponse
	WaitForCallCompletion
)

type VoipClientFSM struct {
	logger        *logger.CustomLogger
	baresipHandle *gobaresip.Baresip
	currentState  int
	// channels for communication with the Baresip instance

	numDialCmds            int
	pendingAudioFileToPlay string
	pendingCallCmdToken    string
	pendingAusrcCmdToken   string
}

func NewVoipClientFSM(logger *logger.CustomLogger, baresipHandle *gobaresip.Baresip) *VoipClientFSM {
	return &VoipClientFSM{
		currentState: WaitingInputs,
	}
}

func (fsm *VoipClientFSM) GetCurrentState() int {
	return fsm.currentState
}

func (fsm *VoipClientFSM) transitionTo(state int) {
	fsm.currentState = state
}

func (fsm *VoipClientFSM) OnNewOutgoingCallRequest(newRequest httpserver.Payload) error {
	fsm.logger.Infof("Received new outgoing call request: %+v", newRequest)

	if fsm.currentState != WaitingInputs {
		// FIXME: perhaps we might instead abort the current operation and start a new call?
		fsm.logger.Warnf("FSM is not in the WaitingInputs state, current state: %d. Ignoring new request.", fsm.currentState)
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
		fsm.logger.Infof("Error dialing: %s", err)
		fsm.transitionTo(WaitingInputs)
	}

	fsm.transitionTo(WaitForDialCmdResponse)

	return nil
}

func (fsm *VoipClientFSM) OnBaresipCmdResponse(response gobaresip.ResponseMsg) error {
	fsm.logger.Infof("Received baresip response: %+v", response)

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
		fsm.logger.Warnf("FSM is not in a WaitForCmdResponse state, current state: %d. Ignoring new request.", fsm.currentState)
		return ErrInvalidState
	}

	return nil
}

func (fsm *VoipClientFSM) OnCallEstablished(event gobaresip.EventMsg) error {
	fsm.logger.Infof("Received call estabilished status update: %+v", event)

	if fsm.currentState != WaitForCallEstablishment {
		fsm.logger.Warnf("FSM is not in the WaitForCallEstablishment state, current state: %d. Ignoring new request.", fsm.currentState)
		return ErrInvalidState
	}

	fsm.pendingAusrcCmdToken = fmt.Sprintf("ausrc_cmd_%d", fsm.numDialCmds)
	err := fsm.baresipHandle.Cmd("ausrc", fmt.Sprintf("aufile,%s", fsm.pendingAudioFileToPlay), fsm.pendingAusrcCmdToken)
	if err != nil {
		fsm.logger.Infof("Error setting audio source to the right file: %s", err)
		fsm.transitionTo(WaitForCallCompletion)
		return nil
	}

	fsm.transitionTo(WaitForAusrcCmdResponse)
	return nil
}

func (fsm *VoipClientFSM) OnCallClosed(event gobaresip.EventMsg) error {
	fsm.logger.Infof("Received call estabilished status update: %+v", event)

	if fsm.currentState != WaitForCallCompletion {
		fsm.logger.Warnf("FSM is not in the WaitForCallCompletion state, current state: %d. Ignoring new request.", fsm.currentState)
		return ErrInvalidState
	}

	fsm.transitionTo(WaitingInputs)

	return nil
}
