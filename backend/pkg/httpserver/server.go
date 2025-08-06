package httpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"voip-client-backend/pkg/config"
	"voip-client-backend/pkg/fsm"
	"voip-client-backend/pkg/logger"

	"github.com/dustin/go-broadcast"
)

const logPrefix = "httpserver"
const dialEndpoint = "/dial"
const httpClientUpdateInterval = 5 * time.Second

type DialPayload struct {
	CalledNumber  string `json:"called_number"`
	CalledContact string `json:"called_contact"`
	MessageTTS    string `json:"message_tts"`
}

type HttpServer struct {
	logger           *logger.CustomLogger
	server           *http.Server
	contactLookupMap map[string]string // Maps contact names to their URIs
	synchronous      bool

	fsmStateSubCh broadcast.Broadcaster
	outCh         chan DialPayload
}

func NewServer(logger *logger.CustomLogger, fsmStatePubSub broadcast.Broadcaster, contacts []config.AddonContact) HttpServer {
	h := HttpServer{
		logger:           logger,
		synchronous:      fsmStatePubSub != nil,
		fsmStateSubCh:    fsmStatePubSub,
		outCh:            make(chan DialPayload),
		contactLookupMap: make(map[string]string),
	}

	// convert slice to map:
	for _, contact := range contacts {
		h.contactLookupMap[contact.Name] = contact.URI
		h.logger.InfoPkgf(logPrefix, "Contact %s added with URI %s", contact.Name, contact.URI)
	}

	// Use the http.NewServeMux() function to create an empty servemux.
	mux := http.NewServeMux()

	// Define the handler for each HTTP endpoint
	mux.HandleFunc(dialEndpoint, func(w http.ResponseWriter, r *http.Request) {
		h.serveDial(w, r)
	})

	// Create a custom HTTP server with timeouts
	h.server = &http.Server{
		Addr:           ":80", // Address to listen on -- this is fixed to the default HTTP port
		Handler:        mux,
		ReadTimeout:    10 * time.Second,  // Maximum duration for reading the entire request, including body
		WriteTimeout:   0,                 // In synchronous mode, it may take a lot of time to complete the write of the response... let's not set any timeout
		IdleTimeout:    120 * time.Second, // Maximum amount of time to wait for the next request when keep-alive is enabled
		MaxHeaderBytes: 1 << 18,           // Max size of request headers, default is 256kB
	}

	return h
}

func (h *HttpServer) waitForFSMState(desiredState fsm.FSMState, w http.ResponseWriter) {
	ch := make(chan interface{})

	// temporarily subscribe to the FSM state changes
	h.fsmStateSubCh.Register(ch)
	defer h.fsmStateSubCh.Unregister(ch)

	// create ticker to provide some update to the HTTP client (HomeAssistant)
	tickerUpdates := time.NewTicker(httpClientUpdateInterval)
	defer tickerUpdates.Stop()

	flusher, ok := w.(http.Flusher)
	if !ok {
		panic("expected http.ResponseWriter to be an http.Flusher")
	}

	h.logger.InfoPkgf(logPrefix, "Now waiting for FSM to reach the [%s] state", desiredState.String())
	for {

		select {
		case stateIntf := <-ch:
			state, ok := stateIntf.(fsm.FSMState)
			if !ok {
				panic("bug")
			}

			// Is it the state we are waiting for?
			if state == desiredState {
				// yes
				h.logger.InfoPkgf(logPrefix, "FSM state changed to the required state [%s]", desiredState.String())
				return
			}

			// keep waiting
			// log disabled: this log is too verbose
			// h.logger.InfoPkgf(logPrefix, "Ignoring FSM state change to [%s]; waiting for FSM to reach state [%s]",
			//   state.String(), desiredState.String())

		case <-tickerUpdates.C:
			// Provide update to the HTTP client
			_, err := io.WriteString(w, "...call ongoing...\n")
			if err != nil {
				h.logger.Warnf("Error writing to HTTP client: %s. Is the client still connected?", err.Error())
				return // stop waiting
			}
			flusher.Flush() // Trigger "chunked" encoding and send a chunk...
		}
	}
}

func (h *HttpServer) serveDial(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		h.logger.InfoPkg(logPrefix, "Replying with HTTP 405: Only POST method is allowed")
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	// Decode the JSON payload from the request body
	var payload DialPayload
	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		h.logger.InfoPkg(logPrefix, "Replying with HTTP 400: invalid JSON payload")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Log the received payload
	h.logger.InfoPkgf(logPrefix, "**********************************") // log marker
	h.logger.InfoPkgf(logPrefix, "Received payload: CalledNumber=%s, CalledContact=%s, MessageTTS=%s\n",
		payload.CalledNumber, payload.CalledContact, payload.MessageTTS)

	// Validate it
	if payload.CalledNumber == "" && payload.CalledContact == "" {
		h.logger.InfoPkg(logPrefix, "Replying with HTTP 400: CalledNumber or CalledContact is required")
		http.Error(w, "CalledNumber or CalledContact is required", http.StatusBadRequest)
		return
	}
	if payload.CalledNumber != "" && payload.CalledContact != "" {
		h.logger.InfoPkg(logPrefix, "Replying with HTTP 400: Only one between CalledNumber and CalledContact can be provided")
		http.Error(w, "Only one between CalledNumber and CalledContact can be provided", http.StatusBadRequest)
		return
	}
	if payload.MessageTTS == "" {
		h.logger.InfoPkg(logPrefix, "Replying with HTTP 400: MessageTTS is required")
		http.Error(w, "MessageTTS is required", http.StatusBadRequest)
		return
	}

	if payload.CalledNumber != "" {
		pattern := `^sip:[^@]+@[^@]+\.[^@]+$`
		valid, err := regexp.MatchString(pattern, payload.CalledNumber)
		if err != nil {
			h.logger.InfoPkg(logPrefix, "Replying with HTTP 500: Error validating CalledNumber")
			http.Error(w, "Error validating CalledNumber", http.StatusInternalServerError)
			return
		}
		if !valid {
			h.logger.InfoPkg(logPrefix, "Replying with HTTP 400: CalledNumber must be in the format sip:<number>@<domain>")
			http.Error(w, "CalledNumber must be in the format sip:<number>@<domain>", http.StatusBadRequest)
			return
		}
	} else if payload.CalledContact != "" {
		// Check if we know about this contact
		contactURI, exists := h.contactLookupMap[payload.CalledContact]
		if !exists {
			h.logger.InfoPkg(logPrefix, "Replying with HTTP 400: Unknown contact")
			http.Error(w, fmt.Sprintf("Unknown contact: %s", payload.CalledContact), http.StatusBadRequest)
			return
		}

		payload.CalledNumber = contactURI // Use the contact URI as the CalledNumber
		h.logger.InfoPkgf(logPrefix, "Using contact URI %s for CalledContact %s", payload.CalledNumber, payload.CalledContact)
	}

	// Send to the output channel
	// h.logger.InfoPkgf(logPrefix, "Sending new call request to FSM")
	h.outCh <- payload
	// h.logger.InfoPkgf(logPrefix, "Sent new call request to the FSM")

	// FIXME wait for FSM to transition out of WaitingInputs at least

	if h.synchronous {
		h.logger.InfoPkgf(logPrefix, "Writing 200 OK and then waiting for processing to complete (synchronous mode) before sending full body to the HTTP client...")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Trailer", "CallCompleted")
		w.WriteHeader(http.StatusOK)

		// wait till the FSM goes back into WaitingInputs state
		h.waitForFSMState(fsm.WaitingInputs, w)

		// then respond to the client
		httpMsg := "Payload was valid and the request has been handled synchronously.\nTTS and call have been attempted. Check addon logs to understand if the TTS/call were successful or not.\nProcessing has been completed and the addon is ready to accept new requests."
		_, _ = io.WriteString(w, httpMsg)
		w.Header().Set("CallCompleted", "True")
		h.logger.InfoPkgf(logPrefix, "Delayed reply with HTTP 200: %s", httpMsg)
	} else {
		// Respond to the client immediately, without any waiting
		httpMsg := "Payload is valid. Initiating TTS generation and outgoing call in asynchronous way."
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, httpMsg)
		h.logger.InfoPkgf(logPrefix, "Immediately replying with HTTP 200 (asynchronous mode): %s", httpMsg)
	}
}

func (h *HttpServer) ListenAndServe() {
	h.logger.InfoPkgf(logPrefix, "Server listening on %s, paths: %s", h.server.Addr, dialEndpoint)
	if err := h.server.ListenAndServe(); err != nil {
		h.logger.Fatalf("Failed to start server: %s", err)
	}
}

// GetInputChannel returns the channel where all requests coming from the HTTP interface are sent
// This is used by the FSM to read the requests and process them
func (h *HttpServer) GetInputChannel() chan DialPayload {
	return h.outCh
}
