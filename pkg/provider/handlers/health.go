// Copyright 2020 OpenFaaS Author(s)
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/openfaas/faasd/pkg"
	"github.com/openfaas/faasd/pkg/provider/catalog"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

const (
	// CPU average overload threshold within one minitues
	CPUOverloadThreshold = 0.80
	// Memory average overload threshold within one minitues
	MemOverloadThreshold = 0.80
)

type CustomHealth struct {
	Overload bool `json:"overload"`
}

// MakeHealthHandler returns 200/OK when healthy
func MakeHealthHandler(localResolver pkg.Resolver, node *catalog.Node) http.HandlerFunc {
	got := make(chan string, 1)
	go localResolver.Get("prometheus", got, time.Second*5)
	ipAddress := <-got
	close(got)
	promClient, _ := api.NewClient(api.Config{
		Address: fmt.Sprintf("http://%s:9090", ipAddress),
	})
	promAPIClient := v1.NewAPI(promClient)
	checkOverload := MeasurePressure(promAPIClient, node)
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		overload_showed := r.URL.Query().Get("overload")
		// If there are no values associated with the key, Get returns the empty string
		if overload_showed == "0" || overload_showed == "" {
			w.WriteHeader(http.StatusOK)
			return
		}
		// overload, err := MeasurePressure(promAPIClient)
		overload := checkOverload()

		jsonOut, err := json.Marshal(CustomHealth{overload})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonOut)
	}
}

// func GetExertnalPressure(resolver proxy.BaseURLResolver) (bool, error) {

// 	hostUrl, err := resolver.Resolve("")
// 	if err != nil {
// 		err := fmt.Errorf("unable to resolve remote host: %v", err)
// 		return false, err
// 	}
// 	healthzUrl := fmt.Sprintf("%s/healthz?overload=1", hostUrl.String())
// 	resp, err := http.Get(healthzUrl)
// 	if err != nil {
// 		err := fmt.Errorf("unable to get health: %v", err)
// 		return false, err
// 	}
// 	defer resp.Body.Close()

// 	// defer upstreamCall.Body.Close()

// 	var health = CustomHealth{Overload: false}

// 	body, _ := io.ReadAll(resp.Body)
// 	err = json.Unmarshal(body, &health)
// 	if err != nil {
// 		log.Printf("Error unmarshalling provider json from body %s. Error %s\n", body, err.Error())
// 	}

// 	return health.Overload, nil

// }

// add the overloaded infomation in it
func MeasurePressure(client v1.API, node *catalog.Node) func() bool {
	// use closure to store the value
	overload := false
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// start query
			// cpu
			cpuQuery := "1 - (rate(node_cpu_seconds_total{mode=\"idle\"}[1m]))"
			CPULoad, err := queryResourceAverageLoad(client, ctx, cpuQuery)
			if err != nil {
				log.Fatalf("CPU usage unavailable from Prometheus: %v", err)
				break
			}
			overload_update := (CPULoad > CPUOverloadThreshold)
			// memory
			// memQuery := "1 - avg_over_time(node_memory_MemAvailable_bytes[1m])/node_memory_MemTotal_bytes"
			memQuery := "1 - ((avg_over_time(node_memory_MemFree_bytes[1m]) + avg_over_time(node_memory_Cached_bytes[1m]) + avg_over_time(node_memory_Buffers_bytes[1m])) / node_memory_MemTotal_bytes)"
			MemLoad, err := queryResourceAverageLoad(client, ctx, memQuery)
			if err != nil {
				log.Fatalf("memory usage unavailable from Prometheus: %v", err)
				break
			}
			overload_update = overload_update || (MemLoad > MemOverloadThreshold)
			time.Sleep(time.Second * 10)
			// fmt.Println("The update overload: ", overload_update)
			// update
			if overload_update != overload {
				overload = overload_update
				node.UpdatePressure(overload)
			}
		}
		// bad exit
		os.Exit(1)
	}()
	return func() bool {
		return overload
	}
	// return overload, nil
}

// func getPrometh
func queryResourceAverageLoad(promClient v1.API, ctx context.Context, query string) (model.SampleValue, error) {

	result, _, err := promClient.Query(ctx, query, time.Now(), v1.WithTimeout(5*time.Second))
	if err != nil {
		err := fmt.Errorf("error querying Prometheus: %v", err)
		return 0, err
	}

	switch {
	case result.Type() == model.ValVector:
		var avgLoad model.SampleValue = 0
		vectorVal := result.(model.Vector)
		for _, elem := range vectorVal {
			avgLoad += elem.Value
		}
		return avgLoad / model.SampleValue(len(vectorVal)), nil
	default:
		err := fmt.Errorf("unexpected value type %q", result.Type())
		return 0, err
	}
}
