# habot
Home Assistant Slack bot

This bot provides the ability to invoke Home Assistant services, and get data about entities and groups of entities using Slack:

- `@homeassistant who's home?`
- `@homeassistant start the vacuum`

etc.

Additionally, it supports confirming commands before executing them. This is helpful if you want confirmation from roommates before doing something disruptive like starting a vacuum or turning lights off.

These actions, either requiring confirmation or not, can also be invoked through HTTP (soon). This allows you to set up an HA automation that results in i.e. asking confirmation before doing something (I.E instead of having an automation that invokes a service directly, you can have it confirm with everyone in Slack that it's OK).

A better written description and manual page coming soon...
