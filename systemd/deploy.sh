#!/bin/bash
set -e

echo Building lnurld...
go install && go build

echo Stopping lnurld service...
sudo systemctl stop lnurld.service

echo Deploying new lnurld...
sudo cp lnurld /usr/local/bin/

echo Starting lnurld service...
sudo systemctl start lnurld.service

echo Success!
