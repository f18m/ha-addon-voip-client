// Package main is the entry point for the DHCP clients web application backend.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"voip-client-backend/pkg/fsm"
	"voip-client-backend/pkg/httpserver"
	"voip-client-backend/pkg/logger"
	"github.com/f18m/go-baresip/pkg/gobaresip"
)

func main() {
	logger := logger.NewCustomLogger("voip-client")
	logger.Info("VOIP client backend starting")

	// Allocate Baresip instance with options
	gb, err := gobaresip.New(
		gobaresip.UseExternalBaresip(),
		gobaresip.SetLogger(logger),
		gobaresip.SetPingInterval(60*time.Second),
	)
	if err != nil {
		logger.Fatalf("init error: %s", err)
	}

	// Run Baresip Serve() method.
	// This is meant to be similar to the http.Serve() method with the difference that
	// it takes an explicit context that can be used to cancel the Baresip instance.
	// In this example, we assume there is an external Baresip process launched
	// so Serve() won't start any background process.
	// The Baresip instance can be terminated at any time using the baresipCancel() function.
	// Communication happens using the event/response channels... keep reading
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
	inputServer := httpserver.NewServer(logger)
	go func() {
		inputServer.ListenAndServe()
	}()

	// Process
	// - BARESIP events: unsolicited messages from baresip, e.g. incoming calls, registrations, etc.
	// - BARESIP responses: responses to commands sent to baresip, e.g. command results
	// - INPUT HTTP requests: messages coming from HomeAssistant via the HTTP server
	// using a simple Finite State Machine (FSM)
	eChan := gb.GetEventChan()
	rChan := gb.GetResponseChan()
	iChan := inputServer.GetInputChannel()
	fsmInstance := fsm.NewVoipClientFSM(logger, gb)
	go func() {
		for {
			select {
			case i, ok := <-iChan:
				if !ok {
					continue
				}
				_ = fsmInstance.OnNewOutgoingCallRequest(i)

			case e, ok := <-eChan:
				if !ok {
					continue
				}
				switch e.Type {
				case gobaresip.UA_EVENT_CALL_ESTABLISHED:
					_ = fsmInstance.OnCallEstablished(e)

				case gobaresip.UA_EVENT_CALL_CLOSED:
					_ = fsmInstance.OnCallClosed(e)

				default:
					logger.Info("Ignoring event %s", e.Type)
				}

			case r, ok := <-rChan:
				if !ok {
					continue
				}
				logger.Info("RESPONSE: " + string(r.RawJSON))
				_ = fsmInstance.OnBaresipCmdResponse(r)
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
