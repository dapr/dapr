#!/bin/bash

[ -z "$1" ] && echo "Usage: $0 [LAST_DAPR_COMMIT]" && exit 1

DAPR_CORE_COMMIT=$1

# Update dapr dependencies for all e2e test apps
cd ./apps
appsroot=`pwd`
appsdirName='apps'
for appdir in * ; do
   if test -f "$appsroot/$appdir/go.mod"; then
      cd $appsroot/$appdir > /dev/null
      go get -u github.com/dapr/dapr@$DAPR_CORE_COMMIT
      go mod tidy
      echo "successfully updated dapr dependency for $appdir"
   fi
done
