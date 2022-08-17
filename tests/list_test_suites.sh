#!/bin/bash

tests=($1)
tests_n=$(expr ${#tests[@]} / $2)

suites=
for((i=0; i < ${#tests[@]}; i+=tests_n))
do
  suites+="\"${tests[@]:i:tests_n}\","
done

suites="[${suites%?}]"
echo $suites
