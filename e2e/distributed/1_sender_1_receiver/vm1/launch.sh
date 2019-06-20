#!/bin/bash
source ~/.profile
cd $GOPATH/src/github.com/actionscore/actions/e2e/distributed/$1/$2
sudo /home/benchmark/.go/bin/go build -o agent
./agent $3 $4 $5 $6
exit $?
