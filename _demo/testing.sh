#!/bin/bash
set -e
cd ../compiler
go install ./cmd/llgo
cd ../_demo
llgo test -v ./runtest
