# VOIP Client Development

Make sure you have read the [Home Assistant addon guide](https://developers.home-assistant.io/docs/add-ons)
before digging into an HomeAssistant addon.

This addon is based on a docker image containing:
* baresip software and its configuration files (adapted for the addon)
* a backend written in golang; see the [backend folder](./backend/); the backend is a bridge between the HomeAssistant automations and Baresip

## How it works

The backend operates a simple Finite State Machine. A slightly-simplified representation of the FSM is:

```mermaid
flowchart TD
    Uninitialized("Uninitialized")
    WaitingUserAgentRegistration("WaitingUserAgentRegistration<br>Add SIP UA to Baresip, which will start registration")
    WaitingInputs("WaitingInputs<br>Waiting for call requests from HA")
    WaitForCallEstablishment("WaitForCallEstablishment<br>Run the TTS engine to produce a WAV file. Ask baresip to start the call, then wait")
    WaitForCallCompletion("WaitForCallCompletion<br>Ask baresip to reproduce the TTS message")

    Uninitialized -- Baresip TCP skt connected --> WaitingUserAgentRegistration
    WaitingUserAgentRegistration -- Baresip Event: Register OK --> WaitingInputs
    WaitingInputs --HTTP Call Request from HA --> WaitForCallEstablishment
    WaitForCallEstablishment -- Baresip call ESTABLISHED event --> WaitForCallCompletion
    WaitForCallCompletion -- Baresip call CLOSED event --> WaitingInputs
    WaitForCallCompletion -- Baresip End-of-File event (send hangup command) --> WaitingInputs
```

