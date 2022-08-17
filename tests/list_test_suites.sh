#!/bin/bash

# List of all tests that should be executed
tests=($1)

# Count of tests
total_tests=${#tests[@]}

# Number of tests by suite
tests_n=$(expr $total_tests / $2)

# Double number (in case of remaining tests and division is not exactly)
double=$(expr $tests_n \* 2)

# divided suites
suites=

for((i=0; i < $total_tests; i+=tests_n))
do
  remaining=$(expr $i + $double)

  # if it can't be divided exactly so the last test will include the remaining tests.
  if [[ $remaining -lt $total_tests ]]; then
    suites+="\"${tests[@]:i:tests_n}\","
  else
    suites+="\"${tests[@]:i:remaining}\","
    break
  fi
done

suites="[${suites%?}]"
echo $suites
