// Package main is the entry point for the DHCP clients web application backend.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"voip-client-backend/pkg/logger"

	"github.com/f18m/go-baresip/pkg/gobaresip"
)

type Payload struct {
	CalledNumber string `json:"called_number"`
	MessageTTS   string `json:"message_tts"`
}

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

	// Process
	// - events: unsolicited messages from baresip, e.g. incoming calls, registrations, etc.
	// - responses: responses to commands sent to baresip, e.g. command results
	// reading from the 2 channels:
	eChan := gb.GetEventChan()
	rChan := gb.GetResponseChan()

	go func() {
		for {
			select {
			case e, ok := <-eChan:
				if !ok {
					continue
				}
				logger.Info("EVENT: " + string(e.RawJSON))

				// your logic goes here

			case r, ok := <-rChan:
				if !ok {
					continue
				}
				logger.Info("RESPONSE: " + string(r.RawJSON))

				// your logic goes here
			}
		}
	}()
	/*
		go func() {
			logger.Info("Reading from stdin...")
			// reader := bufio.NewReader(os.Stdin)
			// text, _ := reader.ReadString('\n')
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				logger.Info("Received from stdin: " + scanner.Text())

				err := gb.CmdDial(scanner.Text())
				if err != nil {
					logger.Infof("Error dialing: %s", err)
				}
			}
		}()*/

	// Define the handler for the HTTP endpoint
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Only accept POST requests
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
			return
		}

		// Decode the JSON payload from the request body
		var payload Payload
		err := json.NewDecoder(r.Body).Decode(&payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Log the received payload
		logger.Infof("Received payload: CalledNumber=%s, MessageTTS=%s\n", payload.CalledNumber, payload.MessageTTS)

		// Dial a new call
		err = gb.CmdDial(payload.CalledNumber)
		if err != nil {
			logger.Infof("Error dialing: %s", err)
		}

		// Respond to the client
		_, _ = fmt.Fprintf(w, "Payload received successfully!")
	})

	// Create a custom HTTP server with timeouts
	httpServer := &http.Server{
		Addr:           ":80",             // Address to listen on
		Handler:        nil,               // Use the default handler (http.DefaultServeMux)
		ReadTimeout:    10 * time.Second,  // Maximum duration for reading the entire request, including body
		WriteTimeout:   10 * time.Second,  // Maximum duration before timing out writes of the response
		IdleTimeout:    120 * time.Second, // Maximum amount of time to wait for the next request when keep-alive is enabled
		MaxHeaderBytes: 1 << 20,           // Max size of request headers, default is 1MB
	}

	// Launch the HTTP server in a goroutine
	go func() {
		port := ":80"
		logger.Infof("Server listening on port %s\n", port)
		if err := httpServer.ListenAndServe(); err != nil {
			logger.Fatalf("Failed to start server: %s\n", err)
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
