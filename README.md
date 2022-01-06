# BritBus

BritBus is a collection of applications that provide realtime & up to date information on bus transport within Great Britain.

Takes advantage of numerous open datasets and combines them into one helpful up to date API & website.

This is the core repository and contains all the Go code for the services that make up BritBus - data importer, web api, historical analyser

## Current Status
BritBus is currently a heavy WIP and only has the following features implemented

* Import bus stops & bus stop groups
* Import bus operators & operator groups
* Very basic web API providing imported data
* Import bus lines
* Import bus timetables (partial)

The following are in the TODO list

* Endpoint for providing timetable for a given stop/line on each day
* Calculate realtime bus progress and stop estimates
* Import fares
* Historical bus reliability analysis
* Subscribe to changes in a bus line

### Areas Supported

#### Stops
All of Great Britain

#### Operators
All of Great Britain

#### Lines/Timetables
Cambridge Stagecoach (partial)

#### Realtime Bus Tracking
None

#### Fares
None