package testdata

const (
	// BaselineMetrics represents the metrics we expect when no additional metrics are enabled
	BaselineMetrics string = `# HELP go_gc_duration_seconds A summary of the GC invocation durations.
# TYPE go_gc_duration_seconds summary
go_gc_duration_seconds{quantile="0"} 0
go_gc_duration_seconds{quantile="0.25"} 0
go_gc_duration_seconds{quantile="0.5"} 0
go_gc_duration_seconds{quantile="0.75"} 0
go_gc_duration_seconds{quantile="1"} 0
go_gc_duration_seconds_sum 0
go_gc_duration_seconds_count 0
# HELP go_goroutines Number of goroutines that currently exist.
# TYPE go_goroutines gauge
go_goroutines 7
# HELP go_info Information about the Go environment.
# TYPE go_info gauge
go_info{version="go1.13.1"} 1
# HELP go_memstats_alloc_bytes Number of bytes allocated and still in use.
# TYPE go_memstats_alloc_bytes gauge
go_memstats_alloc_bytes 1.931736e+06
# HELP go_memstats_alloc_bytes_total Total number of bytes allocated, even if freed.
# TYPE go_memstats_alloc_bytes_total counter
go_memstats_alloc_bytes_total 1.931736e+06
# HELP go_memstats_buck_hash_sys_bytes Number of bytes used by the profiling bucket hash table.
# TYPE go_memstats_buck_hash_sys_bytes gauge
go_memstats_buck_hash_sys_bytes 1.443433e+06
# HELP go_memstats_frees_total Total number of frees.
# TYPE go_memstats_frees_total counter
go_memstats_frees_total 259
# HELP go_memstats_gc_cpu_fraction The fraction of this program's available CPU time used by the GC since the program started.
# TYPE go_memstats_gc_cpu_fraction gauge
go_memstats_gc_cpu_fraction 0
# HELP go_memstats_gc_sys_bytes Number of bytes used for garbage collection system metadata.
# TYPE go_memstats_gc_sys_bytes gauge
go_memstats_gc_sys_bytes 2.240512e+06
# HELP go_memstats_heap_alloc_bytes Number of heap bytes allocated and still in use.
# TYPE go_memstats_heap_alloc_bytes gauge
go_memstats_heap_alloc_bytes 1.931736e+06
# HELP go_memstats_heap_idle_bytes Number of heap bytes waiting to be used.
# TYPE go_memstats_heap_idle_bytes gauge
go_memstats_heap_idle_bytes 6.299648e+07
# HELP go_memstats_heap_inuse_bytes Number of heap bytes that are in use.
# TYPE go_memstats_heap_inuse_bytes gauge
go_memstats_heap_inuse_bytes 3.162112e+06
# HELP go_memstats_heap_objects Number of allocated objects.
# TYPE go_memstats_heap_objects gauge
go_memstats_heap_objects 5853
# HELP go_memstats_heap_released_bytes Number of heap bytes released to OS.
# TYPE go_memstats_heap_released_bytes gauge
go_memstats_heap_released_bytes 6.2930944e+07
# HELP go_memstats_heap_sys_bytes Number of heap bytes obtained from system.
# TYPE go_memstats_heap_sys_bytes gauge
go_memstats_heap_sys_bytes 6.6158592e+07
# HELP go_memstats_last_gc_time_seconds Number of seconds since 1970 of last garbage collection.
# TYPE go_memstats_last_gc_time_seconds gauge
go_memstats_last_gc_time_seconds 0
# HELP go_memstats_lookups_total Total number of pointer lookups.
# TYPE go_memstats_lookups_total counter
go_memstats_lookups_total 0
# HELP go_memstats_mallocs_total Total number of mallocs.
# TYPE go_memstats_mallocs_total counter
go_memstats_mallocs_total 6112
# HELP go_memstats_mcache_inuse_bytes Number of bytes in use by mcache structures.
# TYPE go_memstats_mcache_inuse_bytes gauge
go_memstats_mcache_inuse_bytes 27776
# HELP go_memstats_mcache_sys_bytes Number of bytes used for mcache structures obtained from system.
# TYPE go_memstats_mcache_sys_bytes gauge
go_memstats_mcache_sys_bytes 32768
# HELP go_memstats_mspan_inuse_bytes Number of bytes in use by mspan structures.
# TYPE go_memstats_mspan_inuse_bytes gauge
go_memstats_mspan_inuse_bytes 30192
# HELP go_memstats_mspan_sys_bytes Number of bytes used for mspan structures obtained from system.
# TYPE go_memstats_mspan_sys_bytes gauge
go_memstats_mspan_sys_bytes 32768
# HELP go_memstats_next_gc_bytes Number of heap bytes when next garbage collection will take place.
# TYPE go_memstats_next_gc_bytes gauge
go_memstats_next_gc_bytes 4.473924e+06
# HELP go_memstats_other_sys_bytes Number of bytes used for other system allocations.
# TYPE go_memstats_other_sys_bytes gauge
go_memstats_other_sys_bytes 1.034895e+06
# HELP go_memstats_stack_inuse_bytes Number of bytes in use by the stack allocator.
# TYPE go_memstats_stack_inuse_bytes gauge
go_memstats_stack_inuse_bytes 950272
# HELP go_memstats_stack_sys_bytes Number of bytes obtained from system for stack allocator.
# TYPE go_memstats_stack_sys_bytes gauge
go_memstats_stack_sys_bytes 950272
# HELP go_memstats_sys_bytes Number of bytes obtained from system.
# TYPE go_memstats_sys_bytes gauge
go_memstats_sys_bytes 7.189324e+07
# HELP go_threads Number of OS threads created.
# TYPE go_threads gauge
go_threads 9
# HELP process_cpu_seconds_total Total user and system CPU time spent in seconds.
# TYPE process_cpu_seconds_total counter
process_cpu_seconds_total 0
# HELP process_max_fds Maximum number of open file descriptors.
# TYPE process_max_fds gauge
process_max_fds 4096
# HELP process_open_fds Number of open file descriptors.
# TYPE process_open_fds gauge
process_open_fds 6
# HELP process_resident_memory_bytes Resident memory size in bytes.
# TYPE process_resident_memory_bytes gauge
process_resident_memory_bytes 1.644544e+07
# HELP process_start_time_seconds Start time of the process since unix epoch in seconds.
# TYPE process_start_time_seconds gauge
process_start_time_seconds 1.5813370923e+09
# HELP process_virtual_memory_bytes Virtual memory size in bytes.
# TYPE process_virtual_memory_bytes gauge
process_virtual_memory_bytes 6.53000704e+08
# HELP process_virtual_memory_max_bytes Maximum amount of virtual memory available in bytes.
# TYPE process_virtual_memory_max_bytes gauge
process_virtual_memory_max_bytes -1
# HELP promhttp_metric_handler_requests_in_flight Current number of scrapes being served.
# TYPE promhttp_metric_handler_requests_in_flight gauge
promhttp_metric_handler_requests_in_flight 1
# HELP promhttp_metric_handler_requests_total Total number of scrapes by HTTP status code.
# TYPE promhttp_metric_handler_requests_total counter
promhttp_metric_handler_requests_total{code="200"} 0
promhttp_metric_handler_requests_total{code="500"} 0
promhttp_metric_handler_requests_total{code="503"} 0`

	// GRPCStreamMetrics represents the metrics we expect when gRPC streaming metrics are enabled and their zero value
	GRPCStreamMetrics string = `# HELP grpc_server_handled_total Total number of RPCs completed on the server, regardless of success or failure.
# TYPE grpc_server_handled_total counter
grpc_server_handled_total{grpc_code="Aborted",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="AlreadyExists",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="Canceled",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="DataLoss",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="DeadlineExceeded",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="FailedPrecondition",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="Internal",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="InvalidArgument",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="NotFound",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="OK",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="OutOfRange",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="PermissionDenied",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="ResourceExhausted",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="Unauthenticated",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="Unavailable",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="Unimplemented",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="Unknown",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
# HELP grpc_server_msg_received_total Total number of RPC stream messages received on the server.
# TYPE grpc_server_msg_received_total counter
grpc_server_msg_received_total{grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
# HELP grpc_server_msg_sent_total Total number of gRPC stream messages sent by the server.
# TYPE grpc_server_msg_sent_total counter
grpc_server_msg_sent_total{grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
# HELP grpc_server_started_total Total number of RPCs started on the server.
# TYPE grpc_server_started_total counter
grpc_server_started_total{grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0`

	// GRPCUnaryMetrics represents the metrics we expect when gRPC unary metrics are enabled and their zero value
	GRPCUnaryMetrics string = `# HELP grpc_server_handled_total Total number of RPCs completed on the server, regardless of success or failure.
# TYPE grpc_server_handled_total counter
grpc_server_handled_total{grpc_code="Aborted",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="AlreadyExists",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="Canceled",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="DataLoss",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="DeadlineExceeded",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="FailedPrecondition",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="Internal",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="InvalidArgument",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="NotFound",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="OK",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="OutOfRange",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="PermissionDenied",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="ResourceExhausted",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="Unauthenticated",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="Unavailable",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="Unimplemented",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
grpc_server_handled_total{grpc_code="Unknown",grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
# HELP grpc_server_msg_received_total Total number of RPC stream messages received on the server.
# TYPE grpc_server_msg_received_total counter
grpc_server_msg_received_total{grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
# HELP grpc_server_msg_sent_total Total number of gRPC stream messages sent by the server.
# TYPE grpc_server_msg_sent_total counter
grpc_server_msg_sent_total{grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0
# HELP grpc_server_started_total Total number of RPCs started on the server.
# TYPE grpc_server_started_total counter
grpc_server_started_total{grpc_method="SayHello",grpc_service="helloworld.Greeter",grpc_type="unary"} 0`

	// HTTPServerMetrics represents the metrics we expect when HTTP server metrics are enabled
	HTTPServerMetrics string = `# HELP http_handler_completed_latency_seconds Latency of completed requests.
# TYPE http_handler_completed_latency_seconds histogram
http_handler_completed_latency_seconds_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="0.01"} 1
http_handler_completed_latency_seconds_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="0.03"} 1
http_handler_completed_latency_seconds_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="0.1"} 1
http_handler_completed_latency_seconds_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="0.3"} 1
http_handler_completed_latency_seconds_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="1"} 1
http_handler_completed_latency_seconds_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="3"} 1
http_handler_completed_latency_seconds_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="10"} 1
http_handler_completed_latency_seconds_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="30"} 1
http_handler_completed_latency_seconds_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="100"} 1
http_handler_completed_latency_seconds_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="300"} 1
http_handler_completed_latency_seconds_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="+Inf"} 1
http_handler_completed_latency_seconds_sum{handler="",host="",method="GET",name="daprd",path="/",status="200"} 5.07e-05
http_handler_completed_latency_seconds_count{handler="",host="",method="GET",name="daprd",path="/",status="200"} 1
# HELP http_handler_completed_requests_total Count of completed requests.
# TYPE http_handler_completed_requests_total counter
http_handler_completed_requests_total{handler="",host="",method="GET",name="daprd",path="/",status="200"} 1
# HELP http_handler_request_size_bytes Size of received requests.
# TYPE http_handler_request_size_bytes histogram
http_handler_request_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",le="32"} 1
http_handler_request_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",le="1024"} 1
http_handler_request_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",le="32768"} 1
http_handler_request_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",le="1.048576e+06"} 1
http_handler_request_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",le="3.3554432e+07"} 1
http_handler_request_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",le="1.073741824e+09"} 1
http_handler_request_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",le="+Inf"} 1
http_handler_request_size_bytes_sum{handler="",host="",method="GET",name="daprd",path="/"} 0
http_handler_request_size_bytes_count{handler="",host="",method="GET",name="daprd",path="/"} 1
# HELP http_handler_response_size_bytes Size of sent responses.
# TYPE http_handler_response_size_bytes histogram
http_handler_response_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="32"} 1
http_handler_response_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="1024"} 1
http_handler_response_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="32768"} 1
http_handler_response_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="1.048576e+06"} 1
http_handler_response_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="3.3554432e+07"} 1
http_handler_response_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="1.073741824e+09"} 1
http_handler_response_size_bytes_bucket{handler="",host="",method="GET",name="daprd",path="/",status="200",le="+Inf"} 1
http_handler_response_size_bytes_sum{handler="",host="",method="GET",name="daprd",path="/",status="200"} 0
http_handler_response_size_bytes_count{handler="",host="",method="GET",name="daprd",path="/",status="200"} 1
# HELP http_handler_started_requests_total Count of started requests.
# TYPE http_handler_started_requests_total counter
http_handler_started_requests_total{handler="",host="",method="GET",name="daprd",path="/"} 1`
)
