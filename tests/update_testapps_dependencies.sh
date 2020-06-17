#!/bin/bash

# Update dapr dependencies for all e2e test apps
cd ./apps
for i in `ls`
do
   if test -f "$i/go.mod"
   then
      cd $i > /dev/null
      go get -u github.com/dapr/dapr@master
      echo "successfully updated dapr dependency for $i"
      cd - > /dev/null
   fi
done
