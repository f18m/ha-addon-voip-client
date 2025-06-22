# Home Assistant Add-on: VOIP client

TODO

## How to use

1. Install the [RESTful Command integration](https://www.home-assistant.io/integrations/rest_command). 

2. In your `configuration.yaml` add

```yaml
rest_command:
  # Configuration.yaml entry for VOIP client
  voip_client_call:
    url: http://79957c2e-voip-client.local.hass.io
    method: POST
    headers:
      accept: "application/json, text/html"
    payload: '{"called_number":"{{ called_number }}","message_tts": "{{ message }}"}}'
    content_type:  'application/json; charset=utf-8'
```

3. Create a test automation that runs:

```yaml
automation:
- alias: "Notify to Cellphone"
  triggers:
    - trigger: state
      ... <some trigger you like> ...
  actions:
    - action: rest_command.voip_client_call
      data:
        called_number: <your cellphone number>
        message: "Just a test"
```
