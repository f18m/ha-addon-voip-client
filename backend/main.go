// Package main is the entry point for the DHCP clients web application backend.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"voip-client-backend/pkg/config"
	"voip-client-backend/pkg/fsm"
	"voip-client-backend/pkg/httpserver"
	"voip-client-backend/pkg/logger"
	"voip-client-backend/pkg/tts"

	"github.com/f18m/go-baresip/pkg/gobaresip"
)

const logPrefix = "main"

func main() {
	logger := logger.NewCustomLogger("backend")
	logger.Info("VOIP client backend starting")

	// Read our own config
	cfg, err := config.ReadAddonOptions()
	if err != nil {
		logger.Fatalf("config loading error: %s", err)
	}

	// Allocate Baresip instance with options
	gb, err := gobaresip.New(
		gobaresip.UseExternalBaresip(), // s6-overlay is running baresip in the background
		gobaresip.SetLogger(logger),
		gobaresip.SetPingInterval(1*time.Hour),
	)
	if err != nil {
		logger.Fatalf("baresip init error: %s", err)
	}

	// Run Baresip Serve() method in its own goroutine
	baresipCtx, baresipCancel := context.WithCancel(context.Background())
	go func() {
		err := gb.Serve(baresipCtx)
		if err != nil {
			if errors.Is(err, gobaresip.ErrNoCtrlConn) {
				logger.Fatal("Cannot find the 'baresip' control socket... check the s6 'baresip' service init logs")
			} else {
				logger.Fatalf("baresip exit error: %s", err)
			}
		}
	}()

	// Run Input HTTP server
	inputServer := httpserver.NewServer(logger, cfg.HttpRESTServer.Synchronous, cfg.Contacts)
	go func() {
		inputServer.ListenAndServe()
	}()

	// Init the TTS service
	ttsService := tts.NewTTSService(logger, cfg.TTSEngine.Platform)

	// Process
	// - BARESIP connected event: TCP socket connected
	// - BARESIP events: unsolicited messages from baresip, e.g. incoming calls, registrations, etc.
	// - INPUT HTTP requests: messages coming from HomeAssistant via the HTTP server
	// using a simple Finite State Machine (FSM) -- all business logic is implemented in the FSM
	cChan := gb.GetConnectedChan()
	eChan := gb.GetEventChan()
	iChan := inputServer.GetInputChannel()
	fsmInstance := fsm.NewVoipClientFSM(logger, gb, ttsService)
	statsTicker := time.NewTicker(cfg.GetStatsInterval())

	// Link the HTTP server with the FSM to handle synchronously requests
	if cfg.HttpRESTServer.Synchronous {
		inputServer.SetFSMChannel(fsmInstance.GetStateChannel())
	}

	// Initiate

	go func() {
		for {
			select {
			case c, ok := <-cChan:
				if !ok {
					continue
				}
				if c.Connected {
					_ = fsmInstance.InitializeUserAgent(cfg.VoipProvider.Account, cfg.VoipProvider.Password)
				}

			case i, ok := <-iChan:
				if !ok {
					continue
				}
				_ = fsmInstance.OnNewOutgoingCallRequest(fsm.NewCallRequest{
					CalledNumber: i.CalledNumber,
					MessageTTS:   i.MessageTTS,
				})

			case e, ok := <-eChan:
				if !ok {
					continue
				}
				switch e.Type {
				case gobaresip.UA_EVENT_REGISTER_OK:
					_ = fsmInstance.OnRegisterOk(e)

				case gobaresip.UA_EVENT_REGISTER_FAIL:
					_ = fsmInstance.OnRegisterFail(e)

				case gobaresip.UA_EVENT_CALL_OUTGOING:
					_ = fsmInstance.OnCallOutgoing(e)

				case gobaresip.UA_EVENT_CALL_ESTABLISHED:
					_ = fsmInstance.OnCallEstablished(e)

				case gobaresip.UA_EVENT_CALL_CLOSED:
					_ = fsmInstance.OnCallClosed(e)

				case gobaresip.UA_EVENT_END_OF_FILE:
					_ = fsmInstance.OnEndOfFile(e)

				default:
					logger.InfoPkgf(logPrefix, "Ignoring event type %s", e.Type)
				}

			case <-statsTicker.C:
				// Publish baresip stats to the logger
				stats := gb.GetStats()
				logger.InfoPkgf(logPrefix, "Baresip client stats: %+v", stats)
			}
		}
	}()

	// Show proper shutdown: we will wait for a signal (SIGINT or SIGTERM) to gracefully stop the Baresip instance.
	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)
	go func() {
		sig := <-sigs
		logger.Warnf("** RECEIVED SIGNAL %v **\n", sig)
		done <- true
	}()

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	<-done
	baresipCancel()
	logger.Info("VOIP client backend exiting gracefully")
}
