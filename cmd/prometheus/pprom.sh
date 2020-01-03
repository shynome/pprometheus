#!/bin/sh
exec ./pprometheus --config.file=$PWD/prometheus.yml --web.console.libraries=$PWD/console_libraries --web.console.templates=$PWD/consoles
