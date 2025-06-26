# Home Assistant Add-on: VOIP client

## Rationale

This addon is typically used to interface your smart home with your cellphone, beyond what 
the [Home Assistant Companion App](https://companion.home-assistant.io/) may do.
In particular it's easy to send notifications to both Android and iPhone smartphones using the
[notify](https://www.home-assistant.io/integrations/notify/) platform.
Home Assistant also provides the so-called [Critical Notifications](https://companion.home-assistant.io/docs/notifications/critical-notifications/).

However you might prefer to have Home Assistant make a phone call to you.
For most people, a call is a better attention-catcher compared to a notification.

This addon adds to your Home Assistant a "make call" action that you can use in any automation,
typically to provide critical notifications to yourself or other stakeholders.

## Prerequisites

To use this addon you need to have 

1. An account to a VOIP provider. Maintaining the VOIP account typically requires paying a monthly fee. There are a lot of VOIP providers all over the world. Probably selecting a VOIP provider that is based on your country is a good idea. The author of this addon is using [Orchestra/Irideos](https://orchestra.retelit.it/) which is an Italian VOIP provider.
2. The [RESTful Command integration](https://www.home-assistant.io/integrations/rest_command) installed.
3. One of the [TTS integrations](https://www.home-assistant.io/integrations/#text-to-speech) installed. Please note only "Google Translate" has been tested by the author so far.

## Installation

Follow these steps to get the add-on installed on your system:

1. Add the HA addon store for this addon by clicking here: [![Open your Home Assistant instance and show the add add-on repository dialog with a specific repository URL pre-filled.](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2Ff18m%2Fha-addons-repo)

By doing so you should get to your HomeAssistant configuration page for addon digital archives and you should be asked to add `https://github.com/f18m/ha-addons-repo` to the list. Click "Add".

2. In the list of add-ons, search for "Francesco Montorsi addons" and then the `VOIP Client` add-on and click on that. There is also a "BETA" version available, skip it unless you want to try the latest bugfixes and developments.

3. Click on the "INSTALL" button.

4. Go to the "Configuration" tab of the `VOIP Client` add-on page and populate at minimum the `VOIP Provider` section. You will need to know the [SIP URI](https://en.wikipedia.org/wiki/SIP_URI_scheme) for your account and its password.

5. Finally the [RESTful Command integration](https://www.home-assistant.io/integrations/rest_command) has to be configured on your Home Assistant `configuration.yaml`; open it with your favourite editor and append:

```yaml
rest_command:
  # Configuration.yaml entry for VOIP client
  voip_client_call:
    url: http://79957c2e-voip-client.local.hass.io/dial
    method: POST
    headers:
      accept: "application/json, text/html"
    payload: |
      {
        "called_number": "{{ called_number }}",
        "called_contact": "{{ called_contact }}",
        "message_tts": "{{ message_tts }}"
      }
    content_type: "application/json; charset=utf-8"
    # 2 minutes timeout -- this is important in case you're using the (default) behavior of
    # synchronous REST API: the 'voip_client_call' action will be running for all the time 
    # it takes for the call to be picked up, answered and closed (or just rejected) 
    # by the called party. 2 minutes are typically enough if you're sending short messages.
    timeout: 120

  # This second entry is useful when testing new features with the VOIP client BETA version
  voip_client_call_beta:
    url: http://79957c2e-voip-client-beta.local.hass.io/dial
    method: POST
    headers:
      accept: "application/json, text/html"
    payload: |
      {
        "called_number": "{{ called_number }}",
        "called_contact": "{{ called_contact }}",
        "message_tts": "{{ message_tts }}"
      }
    content_type: "application/json; charset=utf-8"
    # 2 minutes timeout -- this is important in case you're using the (default) behavior of
    # synchronous REST API: the 'voip_client_call' action will be running for all the time 
    # it takes for the call to be picked up, answered and closed (or just rejected) 
    # by the called party. 2 minutes are typically enough if you're sending short messages.
    timeout: 120
```

5. Restart your Home Assistant


## How to use

1. Make sure you have the "RESTful Command integration" integration setup; if not, please read the [Installation](#installation) section.

2. You can now use as automation action a new `rest_command.voip_client_call`:

```yaml
automation:
- alias: "Notify to Cellphone"
  triggers:
    - trigger: state
      ... <some trigger you like> ...
  actions:
    - action: rest_command.voip_client_call
      data:
        called_number: "sip:<number>@<domain>"
        called_contact: ""
        message_tts: "Just a test"
```

Make sure you use for the `called_number` the [SIP URI](https://en.wikipedia.org/wiki/SIP_URI_scheme) format
accepted by your VOIP provider.
Alternatively you can use the `called_contact` field and provide exactly the same contact `name` of a contact
listed in the addon configuration:

```yaml
automation:
- alias: "Notify to Cellphone"
  triggers:
    - trigger: state
      ... <some trigger you like> ...
  actions:
    - action: rest_command.voip_client_call
      data:
        called_number: ""
        called_contact: "John Doe"
        message_tts: "Just a test"
```

Remember that you cannot provide at the same time both `called_number` and `called_contact`, leave empty what you don't want to provide.

