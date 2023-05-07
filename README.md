# Travigo

[![Build, Test, and Deploy](https://github.com/travigo/travigo/actions/workflows/build-and-deploy.yaml/badge.svg)](https://github.com/travigo/travigo/actions/workflows/build-and-deploy.yaml)

Travigo is a collection of applications that provide realtime & up to date information on public transport within Great Britain.

Takes advantage of numerous open datasets and combines them into one helpful up to date API & website.

This is the core repository and contains all the Go code for the services that make up Travigo - data importer, web api, historical analyser

## Current Status
Travigo is currently a heavy WIP and only has the following features implemented

* Import bus stops & bus stop groups
* Import bus operators & operator groups
* Import bus lines
* Import bus timetables
* Endpoint for providing timetable for a given stop on each day
* Calculate realtime bus progress and stop estimates using BODS SIRI-VM

The following are in the TODO list

* Import bus lines from TfE API
* Calculate realtime bus progress and stop estimates using TfE API
* Import fares
* Historical bus reliability analysis
* Subscribe to changes in a bus line
* Highlight potential disruptions on the service or area

### Areas Supported

#### Stops
* All of Great Britain

#### Operators
* All of Great Britain

#### Lines/Timetables
* England
  * Very few Operators data fails to parse
* Scotland & Wales
  * Some Operators do provide data for these and will be included (eg. Stagecoach as a national provider include Scottish & Welsh data)
  * But not guaranteed to have all the data

#### Realtime Bus Tracking
* England
  * Currently tracks up to 19000 bus journeys at a time
* Scotland
  * Stagecoach Services

#### Fares
* None