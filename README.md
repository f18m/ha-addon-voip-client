# Home Assistant Add-on: VOIP Client

![Supports aarch64 Architecture][aarch64-shield] ![Supports amd64 Architecture][amd64-shield] ![Supports armv7 Architecture][armv7-shield] ![Supports i386 Architecture][i386-shield]

## About

This addon runs a `baresip` instance and allow HomeAssistant to interact with it deploying a "middleware" software utility that adapts HomeAssistant inputs to `baresip`.

[aarch64-shield]: https://img.shields.io/badge/aarch64-yes-green.svg
[amd64-shield]: https://img.shields.io/badge/amd64-yes-green.svg
[armv7-shield]: https://img.shields.io/badge/armv7-yes-green.svg
[i386-shield]: https://img.shields.io/badge/i386-yes-green.svg

## Features

* simple-to-use integration with the HomeAssistant Text-To-Speech engine;
* provides a REST server that allows e.g. to expose to HomeAssistant the status of the VOIP client (waiting inputs, dialing, replaying TTS message, etc);
* support contact lists to avoid exposing phone numbers in automations;


## How to Install and How to Configure

Check out the [addon docs](DOCS.md). Open an [issue](https://github.com/f18m/ha-addon-voip-client/issues) if you hit any problem.

## Similar Addons

* [ha-sip](https://github.com/arnonym/ha-plugins): an HomeAssistant addon that provides a VOIP client based on the [pjsip project](https://www.pjsip.org/).
* [dss_voip](https://github.com/sdesalve/hassio-addons/blob/master/dss_voip): an HomeAssistant addon that provides a VOIP client based on the [pjsip project](https://www.pjsip.org/); this particular addon was easier to use compared to ha-sip but has been [abandoned](https://community.home-assistant.io/t/end-of-life-abandoned-dismissed-dss-voip-notifier/130993) in Dec 2024. This is the main reason I started this project.

## Future Developments

See [issues](https://github.com/f18m/ha-addon-voip-client/issues) tagged as "enhancement" to get an idea of next developments.

## Development

See [Home Assistant addon guide](https://developers.home-assistant.io/docs/add-ons)
