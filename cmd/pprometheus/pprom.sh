#!/bin/sh
exec ./ppromethus --config.file=$PWD/promethus.yml --web.console.libraries=$PWD/console_libraries --web.console.templates=$PWD/consoles
