hap-z2m Bridge
===============

A HomeKit <-> Zigbee2MQTT bridge written in Go, so I don't have to install more NodeJS rubbish.
It is essentially a Homebridge + homebridge-z2m replacement.
It compiles down to a <10 MB static binary instead of another 200++ MB Docker container.
It uses the [hap library](https://github.com/brutella/hap) for interfacing with HomeKit.

It is quite barebones, so there is no configuration for the bridge, apart from the MQTT server & credentials.
The bridge configures and exposes devices based on z2m's MQTT messages.

Currently only supports the types of Zigbee devices I have:

- Climate sensors (temp, humidity)
- Contact sensors
- Motion sensors
- Wall switch
- Dimmer, or dimmable light bulbs

If you do use this software, note that it's in development and may contains bugs,
or may even burn your house down. I offer no warranty, but you are welcome to file bugs.


Building
=========

To compile `hap-z2m`, use:

    go build -v -trimpath -ldflags="-s -w" ./cmd/...

Configuration
==============

The MQTT broker that Zigbee2MQTT connects to is specified using the following:

- Server
- Username
- Password

Additionally, these options control networking aspects for the bridge:

- `ListenAddr` can be used to bind to a specific port and/or address, 
  useful for selecting a fixed port for firewall rules.

- `Interfaces` limit which interfaces mDNS broadcasts appear on, 
  and which addresses to use when broadcasting.

These settings are optional and can be left blank.

The pairing code for the server is generated dynamically on first startup
and printed to the console (or systemd journal). Alternatively, you can specify
it using `Pin` in the config file to have it fixed.


Debugging
==========

Checking the Zigbee2MQTT persisted state:

    jq -r 'map_values(@base64d)' < /var/lib/hapz2m/db/z2m_state

License
========

hap-z2m is licensed under the GNU General Public License v3.

Copyright (C) 2023-24 Darell Tan

This program is free software: you can redistribute it and/or modify it under
the terms of the GNU General Public License as published by the Free Software
Foundation, either version 3 of the License, or (at your option) any later
version.

This program is distributed in the hope that it will be useful, but WITHOUT ANY
WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A
PARTICULAR PURPOSE. See the GNU General Public License for more details.

You should have received a copy of the GNU General Public License along with
this program. If not, see <https://www.gnu.org/licenses/>.

