/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/exp/slices"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	daprClientSDK "github.com/dapr/go-sdk/client"
	runtimev1pb "github.com/dapr/go-sdk/dapr/proto/runtime/v1"
)

var (
	appPort, daprGRPCPort, daprHTTPPort int
	sdkClient                           daprClientSDK.Client
	httpClient                          = NewHTTPClient()
)

type resettableProto interface {
	proto.Message
	Reset()
}

// Handles HTTP tests
func httpTestRouter(r chi.Router) {
	// POST /test/http/{component}/encrypt?key={key}&alg={alg}
	r.Post("/encrypt", func(w http.ResponseWriter, r *http.Request) {
		u := fmt.Sprintf("http://localhost:%v/v1.0-alpha1/crypto/%s/encrypt", daprHTTPPort, chi.URLParam(r, "component"))
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPut, u, r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("error creating request: %v", err), http.StatusInternalServerError)
			return
		}
		req.Header.Set("dapr-key-name", r.URL.Query().Get("key"))
		req.Header.Set("dapr-key-wrap-algorithm", r.URL.Query().Get("alg"))

		res, err := httpClient.Do(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("error sending request: %v", err), http.StatusInternalServerError)
			return
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			http.Error(w, fmt.Sprintf("invalid status code: %v", res.StatusCode), http.StatusInternalServerError)
			return
		}

		n, err := io.Copy(w, res.Body)
		log.Printf("Encrypted %d bytes. Error: %v", n, err)
	})

	// POST /test/http/{component}/decrypt
	r.Post("/decrypt", func(w http.ResponseWriter, r *http.Request) {
		u := fmt.Sprintf("http://localhost:%v/v1.0-alpha1/crypto/%s/decrypt", daprHTTPPort, chi.URLParam(r, "component"))
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPut, u, r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("error creating request: %v", err), http.StatusInternalServerError)
			return
		}

		res, err := httpClient.Do(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("error sending request: %v", err), http.StatusInternalServerError)
			return
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			http.Error(w, fmt.Sprintf("invalid status code: %v", res.StatusCode), http.StatusInternalServerError)
			return
		}

		n, err := io.Copy(w, res.Body)

		log.Printf("Decrypted %d bytes. Error: %v", n, err)
	})
}

// Handles gRPC tests
func grpcTestRouter(r chi.Router) {
	// POST /test/grpc/{component}/encrypt?key={key}&alg={alg}
	r.Post("/encrypt", func(w http.ResponseWriter, r *http.Request) {
		enc, err := sdkClient.Encrypt(r.Context(), r.Body, daprClientSDK.EncryptOptions{
			ComponentName:    chi.URLParam(r, "component"),
			KeyName:          r.URL.Query().Get("key"),
			KeyWrapAlgorithm: r.URL.Query().Get("alg"),
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("error encrypting data: %v", err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		n, err := io.Copy(w, enc)

		log.Printf("Encrypted %d bytes. Error: %v", n, err)
	})

	// POST /test/grpc/{component}/decrypt
	r.Post("/decrypt", func(w http.ResponseWriter, r *http.Request) {
		dec, err := sdkClient.Decrypt(r.Context(), r.Body, daprClientSDK.DecryptOptions{
			ComponentName: chi.URLParam(r, "component"),
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("error decrypting data: %v", err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		n, err := io.Copy(w, dec)

		log.Printf("Decrypted %d bytes. Error: %v", n, err)
	})
}

func grpcSubtleTestRouter(r chi.Router) {
	grpcClient := sdkClient.GrpcClient()

	// POST /test/subtle/grpc/getkey
	r.Post("/getkey", subtleOpGRPC(grpcClient, &runtimev1pb.SubtleGetKeyRequest{}, grpcClient.SubtleGetKeyAlpha1))
	// POST /test/subtle/grpc/encrypt
	r.Post("/encrypt", subtleOpGRPC(grpcClient, &runtimev1pb.SubtleEncryptRequest{}, grpcClient.SubtleEncryptAlpha1))
	// POST /test/subtle/grpc/decrypt
	r.Post("/decrypt", subtleOpGRPC(grpcClient, &runtimev1pb.SubtleDecryptRequest{}, grpcClient.SubtleDecryptAlpha1))
	// POST /test/subtle/grpc/wrapkey
	r.Post("/wrapkey", subtleOpGRPC(grpcClient, &runtimev1pb.SubtleWrapKeyRequest{}, grpcClient.SubtleWrapKeyAlpha1))
	// POST /test/subtle/grpc/unwrapkey
	r.Post("/unwrapkey", subtleOpGRPC(grpcClient, &runtimev1pb.SubtleUnwrapKeyRequest{}, grpcClient.SubtleUnwrapKeyAlpha1))
	// POST /test/subtle/grpc/sign
	r.Post("/sign", subtleOpGRPC(grpcClient, &runtimev1pb.SubtleSignRequest{}, grpcClient.SubtleSignAlpha1))
	// POST /test/subtle/grpc/verify
	r.Post("/verify", subtleOpGRPC(grpcClient, &runtimev1pb.SubtleVerifyRequest{}, grpcClient.SubtleVerifyAlpha1))
}

func httpSubtleTestRouter(r chi.Router) {
	// POST /test/subtle/http/getkey
	r.Post("/getkey", subtleOpHTTP("getkey", &runtimev1pb.SubtleGetKeyRequest{}, &runtimev1pb.SubtleGetKeyResponse{}))
	// POST /test/subtle/http/encrypt
	r.Post("/encrypt", subtleOpHTTP("encrypt", &runtimev1pb.SubtleEncryptRequest{}, &runtimev1pb.SubtleEncryptResponse{}))
	// POST /test/subtle/http/decrypt
	r.Post("/decrypt", subtleOpHTTP("decrypt", &runtimev1pb.SubtleDecryptRequest{}, &runtimev1pb.SubtleDecryptResponse{}))
	// POST /test/subtle/http/wrapkey
	r.Post("/wrapkey", subtleOpHTTP("wrapkey", &runtimev1pb.SubtleWrapKeyRequest{}, &runtimev1pb.SubtleWrapKeyResponse{}))
	// POST /test/subtle/http/unwrapkey
	r.Post("/unwrapkey", subtleOpHTTP("unwrapkey", &runtimev1pb.SubtleUnwrapKeyRequest{}, &runtimev1pb.SubtleUnwrapKeyResponse{}))
	// POST /test/subtle/http/sign
	r.Post("/sign", subtleOpHTTP("sign", &runtimev1pb.SubtleSignRequest{}, &runtimev1pb.SubtleSignResponse{}))
	// POST /test/subtle/http/verify
	r.Post("/verify", subtleOpHTTP("verify", &runtimev1pb.SubtleVerifyRequest{}, &runtimev1pb.SubtleVerifyResponse{}))
}

// Note: these methods are not thread-safe
func subtleOpGRPC[
	Req resettableProto,
	Res proto.Message,
](
	grpcClient runtimev1pb.DaprClient,
	req Req,
	fn func(ctx context.Context, req Req, opts ...grpc.CallOption) (Res, error),
) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		req.Reset()

		reqData, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("error reading body: %v", err), http.StatusInternalServerError)
			return
		}

		err = protojson.Unmarshal(reqData, req)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to unmarshal request data with protojson: %v", err), http.StatusInternalServerError)
			return
		}

		res, err := fn(r.Context(), req)
		if err != nil {
			http.Error(w, fmt.Sprintf("error performing operation: %v", err), http.StatusInternalServerError)
			return
		}

		resData, err := protojson.Marshal(res)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed marshal response data with protojson: %v", err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(resData)
	}
}

type subtlecryptoReq interface {
	SetComponentName(name string)
	GetComponentName() string
	resettableProto
}

// Note: these methods are not thread-safe
func subtleOpHTTP[
	Req subtlecryptoReq,
	Res resettableProto,
](
	method string,
	req Req,
	res Res,
) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		req.Reset()
		res.Reset()

		reqData, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("error reading body: %v", err), http.StatusInternalServerError)
			return
		}

		err = protojson.Unmarshal(reqData, req)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to unmarshal request data with protojson: %v", err), http.StatusInternalServerError)
			return
		}

		u := fmt.Sprintf("http://localhost:%v/v1.0-alpha1/subtlecrypto/%s/%s", daprHTTPPort, req.GetComponentName(), method)
		res, err := httpClient.Post(u, "application/json", bytes.NewReader(reqData))
		if err != nil {
			http.Error(w, fmt.Sprintf("error performing operation: %v", err), http.StatusInternalServerError)
			return
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			http.Error(w, fmt.Sprintf("invalid response status code: %d", res.StatusCode), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		io.Copy(w, res.Body)
	}
}

type metadataResponse struct {
	EnabledFeatures []string `json:"enabledFeatures"`
}

// TODO: Remove this handler when SubtleCrypto is finalized
func handleSubtleCryptoEnabled(w http.ResponseWriter, r *http.Request) {
	var metadata metadataResponse
	res, err := httpClient.Get(fmt.Sprintf("http://localhost:%v/v1.0/metadata", daprHTTPPort))
	if err != nil {
		http.Error(w, fmt.Sprintf("could not get sidecar metadata: %v", err), http.StatusInternalServerError)
		return
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("invalid response status code from metadata endpoint: %d", res.StatusCode), http.StatusInternalServerError)
		return
	}

	err = json.NewDecoder(res.Body).Decode(&metadata)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not get sidecar metadata from JSON: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	if len(metadata.EnabledFeatures) > 0 && slices.Contains(metadata.EnabledFeatures, "_SubtleCrypto") {
		fmt.Fprint(w, "1")
	} else {
		fmt.Fprint(w, "0")
	}
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := chi.NewRouter()

	// Log requests and their processing time
	router.Use(LoggerMiddleware)

	// Handler for / which returns a "hello world" like message
	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "👋")
	})

	// Handler for /getSubtleCryptoEnabled that returns whether subtle crypto APIs are enabled
	router.Get("/getSubtleCryptoEnabled", handleSubtleCryptoEnabled)

	// Run tests
	router.Route("/test/grpc/{component}", grpcTestRouter)
	router.Route("/test/http/{component}", httpTestRouter)
	router.Route("/test/subtle/grpc", grpcSubtleTestRouter)
	router.Route("/test/subtle/http", httpSubtleTestRouter)

	return router
}

func main() {
	// Ports
	appPort = PortFromEnv("APP_PORT", 3000)
	daprGRPCPort = PortFromEnv("DAPR_GRPC_PORT", 50001)
	daprHTTPPort = PortFromEnv("DAPR_HTTP_PORT", 3500)
	log.Printf("Dapr ports: gRPC: %d, HTTP: %d", daprGRPCPort, daprHTTPPort)

	// Connect to gRPC
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	conn, err := grpc.DialContext(
		ctx,
		net.JoinHostPort("127.0.0.1", strconv.Itoa(daprGRPCPort)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	cancel()
	if err != nil {
		log.Fatalf("Error connecting to Dapr's gRPC endpoint on port %d: %v", daprGRPCPort, err)
	}

	// Create a Dapr SDK client
	sdkClient = daprClientSDK.NewClientWithConnection(conn)

	// Start the server
	log.Printf("Starting application on  http://localhost:%d", appPort)
	StartServer(appPort, appRouter)
}
