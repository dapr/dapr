#!/bin/bash

# Update dapr dependencies for all e2e test apps
cd ./apps
appsroot=`pwd`
appsdirName='apps'
for appdir in * ; do
   if test -f "$appsroot/$appdir/go.mod"; then
      cd $appsroot/$appdir > /dev/null
      go get -u github.com/dapr/dapr@d48d25e83cf4c85f566e9423340993dc517d8ac3
      go mod tidy
      echo "successfully updated dapr dependency for $appdir"
   fi
done
