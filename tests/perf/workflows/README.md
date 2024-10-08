# Dapr Workflows Performance Tests

This project aims to test the performance of Dapr workflows under various conditions.

## Glossary

VU (Virtual User): The number of concurrent workflows running at a time
Iterations: Total number of workflow runs
Req_Duration: Time taken to complete a workflow run.
Sidecar: Dapr Sidecar

## Test plan

For the test, a single instance of an application with Dapr sidecar was deployed on AKS and then different workflows were created using K6 framework. For each of the tests, app memory as well as Dapr sidecar memory limit was capped at 800 Mb. 

### TestWorkflowWithConstantVUs

Workflow Description

* Contains 5 chained activities
* Each activity performs numeric calculation and the result is sent back to workflow

Test Description

* Run workflow with concurrency of 50 and total runs 500
* Calculate memory and cpu usage after the run
* Run the whole set again (total 5 times) without restarting the application
* Monitor sidecar memory and cpu usage after each set

### TestWorkflowWithConstantIterations

Workflow Description

* Contains 5 chained activities
* Each activity performs numeric calculation and the result is sent back to workflow.

Test Description

* Run workflow with concurrency of 50 and total runs 500
* Restart the application
* Run worklfow with concurrency of 100 and total runs 500
* Restart the application
* Run worklfow with concurrency of 150 and total runs 500
* Monitor memory/cpu usage as well as req_duration(time taken to complete a workflow) after each set

### TestSeriesWorkflowWithMaxVUs

Workflow Description

* Contains 5 chained activities
* Each activity performs numeric calculation and the result is sent back to workflow

Test Description

* Run workflow with concurrency of 500 and for total runs 3000
* Verify there are no errors and all workflow runs pass
* Monitor memory/cpu usage as well as req_duration(time taken to complete a workflow) after the run 

### TestParallelWorkflowWithMaxVUs

Workflow Description

* Contains 5 activities in parallel
* Each activity performs numeric calculation and the result is aggregated at the workflow

Test Description

* Run workflow with concurrency of 110 and total runs 550
* Verify there are no errors and all workflow runs pass
* Monitor memory/cpu usage as well as req_duration(time taken to complete a workflow) after the run

### TestWorkflowWithDifferentPayloads

* Contains 3 activities in series
* Activity 1 saves data of given size in the configured statestore
* Activity 2 gets saved data from the statestore using key
* Activity 3 deletes data from the statestore
* Workflow takes data size as input and passes a string of that size as payload to the activity

Test Description

* Run workflow with payload size of 10KB with concurrency of 50 and total runs 500
* Restart the application
* Run workflow with payload size of 50KB with concurrency of 50 and total runs 500
* Restart the application
* Run workflow with payload size of 100KB with concurrency of 50 and total runs 500
* Monitor memory usage after each run