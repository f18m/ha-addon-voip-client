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

	broadcast "github.com/dustin/go-broadcast"
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
	baresipConn, err := gobaresip.New(
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
		err := baresipConn.Serve(baresipCtx)
		if err != nil {
			if errors.Is(err, gobaresip.ErrNoCtrlConn) {
				logger.Fatal("Cannot find the 'baresip' control socket... check the s6 'baresip' service init logs")
			} else {
				logger.Fatalf("baresip exit error: %s", err)
			}
		}
	}()

	// PUB-SUB channel used from FSM to publish its state changes to...whoever is interested
	broadcaster := broadcast.NewBroadcaster(100)

	// Run the input HTTP server, which can process HTTP API requests coming from HomeAssistant.
	var inputServer httpserver.HttpServer
	if cfg.HttpRESTServer.Synchronous {
		inputServer = httpserver.NewServer(logger, broadcaster, cfg.Contacts)
	} else {
		inputServer = httpserver.NewServer(logger, nil, cfg.Contacts)
	}
	go func() {
		inputServer.ListenAndServe()
	}()

	// Init the TTS service
	ttsService := tts.NewTTSService(logger, cfg.TTSEngine.Platform)

	// Process
	// - BARESIP connected event: TCP socket connected
	// - BARESIP events: unsolicited messages from baresip, e.g. incoming calls, registrations, etc.
	// - INPUT HTTP requests: messages coming from HomeAssistant via the HTTP server
	// - TICKER events: periodic events to check the status of the calls and the Baresip client
	// using a simple Finite State Machine (FSM) -- all business logic is implemented in the FSM
	cChan := baresipConn.GetConnectedChan()
	eChan := baresipConn.GetEventChan()
	iChan := inputServer.GetInputChannel()
	fsmInstance := fsm.NewVoipClientFSM(logger, baresipConn, ttsService, broadcaster, cfg.GetVoiceCallMaxDuration())
	statsTicker := time.NewTicker(cfg.GetStatsInterval())
	timeoutTicker := time.NewTicker(cfg.GetVoiceCallMaxDuration() / 10)

	// Run the FSM in its own goroutine

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
				stats := baresipConn.GetStats()
				logger.InfoPkgf(logPrefix, "Baresip client stats: %+v", stats)

			case <-timeoutTicker.C:
				// Let the FSM check if there are any calls that have been established for too long
				fsmInstance.OnTimeoutTicker()
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
