#!/bin/bash

# Update dapr dependencies for all e2e test apps
cd ./apps
appsroot=`pwd`
appsdirName='apps'
for appdir in * ; do
   if test -f "$appsroot/$appdir/go.mod"; then
      cd $appsroot/$appdir > /dev/null
      go get -u github.com/dapr/dapr@master
      go mod tidy
      echo "successfully updated dapr dependency for $appdir"
   fi
done
