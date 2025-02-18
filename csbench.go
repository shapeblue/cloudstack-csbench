// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package main

import (
	"csbench/domain"
	"csbench/network"
	"csbench/vm"
	"csbench/volume"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"csbench/apirunner"
	"csbench/config"

	log "github.com/sirupsen/logrus"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/montanaflynn/stats"
	"github.com/sourcegraph/conc/pool"
)

var (
	profiles = make(map[int]*config.Profile)
)

type Result struct {
	Success  bool
	Duration float64
}

func init() {
	logFile, err := os.OpenFile("csmetrics.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Failed to create log file: %v", err)
	}

	mw := io.MultiWriter(os.Stdout, logFile)

	log.SetOutput(mw)
}

func readConfigurations(configFile string) map[int]*config.Profile {
	profiles, err := config.ReadProfiles(configFile)
	if err != nil {
		log.Fatal("Error reading profiles:", err)
	}

	return profiles
}

func logConfigurationDetails(profiles map[int]*config.Profile) {
	apiURL := config.URL
	iterations := config.Iterations
	page := config.Page
	pagesize := config.PageSize
	host := config.Host

	userProfileNames := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		userProfileNames = append(userProfileNames, profile.Name)
	}

	fmt.Printf("\n\n\033[1;34mBenchmarking the CloudStack environment [%s] with the following configuration\033[0m\n\n", apiURL)
	fmt.Printf("Management server : %s\n", host)
	fmt.Printf("Roles : %s\n", strings.Join(userProfileNames, ","))
	fmt.Printf("Iterations : %d\n", iterations)
	fmt.Printf("Page : %d\n", page)
	fmt.Printf("PageSize : %d\n\n", pagesize)

	log.Infof("Found %d profiles in the configuration: ", len(profiles))
	log.Infof("Management server : %s", host)
}

func logReport() {
	fmt.Printf("\n\n\nLog file : csmetrics.log\n")
	fmt.Printf("Reports directory per API : report/%s/\n", config.Host)
	fmt.Printf("Number of APIs : %d\n", apirunner.APIscount)
	fmt.Printf("Successful APIs : %d\n", apirunner.SuccessAPIs)
	fmt.Printf("Failed APIs : %d\n", apirunner.FailedAPIs)
	fmt.Printf("Time in seconds per API: %.2f (avg)\n", apirunner.TotalTime/float64(apirunner.APIscount))
	fmt.Printf("\n\n\033[1;34m--------------------------------------------------------------------------------\033[0m\n" +
		"                            Done with benchmarking\n" +
		"\033[1;34m--------------------------------------------------------------------------------\033[0m\n\n")
}

func getSamples(results []*Result) (stats.Float64Data, stats.Float64Data, stats.Float64Data) {
	var allExecutionsSample stats.Float64Data
	var successfulExecutionSample stats.Float64Data
	var failedExecutionSample stats.Float64Data

	for _, result := range results {
		duration := math.Round(result.Duration*1000) / 1000
		allExecutionsSample = append(allExecutionsSample, duration)
		if result.Success {
			successfulExecutionSample = append(successfulExecutionSample, duration)
		} else {
			failedExecutionSample = append(failedExecutionSample, duration)
		}
	}

	return allExecutionsSample, successfulExecutionSample, failedExecutionSample
}

func getRowFromSample(key string, sample stats.Float64Data) table.Row {
	min, _ := sample.Min()
	min = math.Round(min*1000) / 1000
	max, _ := sample.Max()
	max = math.Round(max*1000) / 1000
	mean, _ := sample.Mean()
	mean = math.Round(mean*1000) / 1000
	median, _ := sample.Median()
	median = math.Round(median*1000) / 1000
	percentile90, _ := sample.Percentile(90)
	percentile90 = math.Round(percentile90*1000) / 1000
	percentile95, _ := sample.Percentile(95)
	percentile95 = math.Round(percentile95*1000) / 1000
	percentile99, _ := sample.Percentile(99)
	percentile99 = math.Round(percentile99*1000) / 1000

	return table.Row{key, len(sample), min, max, mean, median, percentile90, percentile95, percentile99}
}

/*
This function will generate a report with the following details:
 1. Total Number of executions
 2. Number of successful executions
 3. Number of failed exections
 4. Different statistics like min, max, avg, median, 90th percentile, 95th percentile, 99th percentile for above 3

Output format:
 1. CSV
 2. TSV
 3. Table
*/
func generateReport(results map[string][]*Result, format string, outputFile string) {
	fmt.Println("Generating report")

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"Type", "Count", "Min", "Max", "Avg", "Median", "90th percentile", "95th percentile", "99th percentile"})

	for key, result := range results {
		allExecutionsSample, successfulExecutionSample, failedExecutionSample := getSamples(result)
		t.AppendRow(getRowFromSample(fmt.Sprintf("%s - All", key), allExecutionsSample))

		if failedExecutionSample.Len() != 0 {
			t.AppendRow(getRowFromSample(fmt.Sprintf("%s - Successful", key), successfulExecutionSample))
			t.AppendRow(getRowFromSample(fmt.Sprintf("%s - Failed", key), failedExecutionSample))
		}
	}

	if outputFile != "" {
		f, err := os.Create(outputFile)
		if err != nil {
			log.Error("Error creating file: ", err)
		}
		defer f.Close()
		t.SetOutputMirror(f)
	}
	switch format {
	case "csv":
		t.RenderCSV()
	case "tsv":
		t.RenderTSV()
	case "table":
		t.Render()
	}
}

func main() {
	dbprofile := flag.Int("dbprofile", 0, "DB profile number")
	create := flag.Bool("create", false, "Create resources")
	benchmark := flag.Bool("benchmark", false, "Benchmark list APIs")
	domainFlag := flag.Bool("domain", false, "Create domain")
	limitsFlag := flag.Bool("limits", false, "Update limits to -1")
	networkFlag := flag.Bool("network", false, "Create shared network")
	vmFlag := flag.Bool("vm", false, "Deploy VMs")
	volumeFlag := flag.Bool("volume", false, "Attach Volumes to VMs")
	tearDown := flag.Bool("teardown", false, "Tear down all subdomains")
	workers := flag.Int("workers", 10, "number of workers to use while creating resources")
	format := flag.String("format", "table", "Format of the report (csv, tsv, table). Valid only for create")
	outputFile := flag.String("output", "", "Path to output file. Valid only for create")
	configFile := flag.String("config", "config/config", "Path to config file")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go run csmetrictool.go -dbprofile <DB profile number>\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if !(*create || *benchmark || *tearDown) {
		log.Fatal("Please provide one of the following options: -create, -benchmark, -teardown")
	}

	if *create && !(*domainFlag || *limitsFlag || *networkFlag || *vmFlag || *volumeFlag) {
		log.Fatal("Please provide one of the following options with create: -domain, -limits, -network, -vm, -volume")
	}

	switch *format {
	case "csv", "tsv", "table":
		// valid format, continue
	default:
		log.Fatal("Invalid format. Please provide one of the following: csv, tsv, table")
	}

	if *dbprofile < 0 {
		log.Fatal("Invalid DB profile number. Please provide a positive integer.")
	}

	profiles = readConfigurations(*configFile)
	apiURL := config.URL
	iterations := config.Iterations
	page := config.Page
	pagesize := config.PageSize

	if *create {
		results := createResources(domainFlag, limitsFlag, networkFlag, vmFlag, volumeFlag, workers)
		generateReport(results, *format, *outputFile)
	}

	if *benchmark {
		log.Infof("\nStarted benchmarking the CloudStack environment [%s]", apiURL)

		logConfigurationDetails(profiles)

		for i, profile := range profiles {
			userProfileName := profile.Name
			log.Infof("Using profile %d.%s for benchmarking", i, userProfileName)
			fmt.Printf("\n\033[1;34m============================================================\033[0m\n")
			fmt.Printf("                    Profile: [%s]\n", userProfileName)
			fmt.Printf("\033[1;34m============================================================\033[0m\n")
			apirunner.RunAPIs(userProfileName, apiURL, profile.ApiKey, profile.SecretKey, profile.Expires, profile.SignatureVersion, iterations, page, pagesize, *dbprofile)
		}
		logReport()

		log.Infof("Done with benchmarking the CloudStack environment [%s]", apiURL)
	}

	if *tearDown {
		tearDownEnv()
	}
}

func createResources(domainFlag, limitsFlag, networkFlag, vmFlag, volumeFlag *bool, workers *int) map[string][]*Result {
	apiURL := config.URL

	for _, profile := range profiles {
		if profile.Name == "admin" {

			numVmsPerNetwork := config.NumVms
			numVolumesPerVM := config.NumVolumes

			cs := cloudstack.NewAsyncClient(apiURL, profile.ApiKey, profile.SecretKey, false)

			var results = make(map[string][]*Result)

			if *domainFlag {
				workerPool := pool.NewWithResults[*Result]().WithMaxGoroutines(*workers)
				results["domain"] = createDomains(workerPool, cs, config.ParentDomainId, config.NumDomains)
			}

			if *limitsFlag {
				workerPool := pool.NewWithResults[*Result]().WithMaxGoroutines(*workers)
				results["limits"] = updateLimits(workerPool, cs, config.ParentDomainId)
			}

			if *networkFlag {
				workerPool := pool.NewWithResults[*Result]().WithMaxGoroutines(*workers)
				results["network"] = createNetwork(workerPool, cs, config.ParentDomainId)
			}

			if *vmFlag {
				workerPool := pool.NewWithResults[*Result]().WithMaxGoroutines(*workers)
				results["vm"] = createVms(workerPool, cs, config.ParentDomainId, numVmsPerNetwork)
			}

			if *volumeFlag {
				workerPool := pool.NewWithResults[*Result]().WithMaxGoroutines(*workers)
				results["volume"] = createVolumes(workerPool, cs, config.ParentDomainId, numVolumesPerVM)
			}

			return results
		}
	}
	return nil
}

func createDomains(workerPool *pool.ResultPool[*Result], cs *cloudstack.CloudStackClient, parentDomainId string, count int) []*Result {
	progressMarker := int(math.Ceil(float64(count) / 10.0))
	start := time.Now()
	log.Infof("Creating %d domains", count)
	for i := 0; i < count; i++ {
		if (i+1)%progressMarker == 0 {
			log.Infof("Created %d domains", i+1)
		}
		workerPool.Go(func() *Result {
			taskStart := time.Now()
			dmn, err := domain.CreateDomain(cs, parentDomainId)
			if err != nil {
				return &Result{
					Success:  false,
					Duration: time.Since(taskStart).Seconds(),
				}
			}
			_, err = domain.CreateAccount(cs, dmn.Id)
			if err != nil {
				return &Result{
					Success:  false,
					Duration: time.Since(taskStart).Seconds(),
				}
			}

			return &Result{
				Success:  true,
				Duration: time.Since(taskStart).Seconds(),
			}
		})
	}
	res := workerPool.Wait()
	log.Infof("Created %d domains in %.2f seconds", count, time.Since(start).Seconds())
	return res
}

func updateLimits(workerPool *pool.ResultPool[*Result], cs *cloudstack.CloudStackClient, parentDomainId string) []*Result {
	log.Infof("Fetching subdomains for domain %s", parentDomainId)
	domains := domain.ListSubDomains(cs, parentDomainId)
	accounts := make([]*cloudstack.Account, 0)
	for _, dmn := range domains {
		accounts = append(accounts, domain.ListAccounts(cs, dmn.Id)...)
	}

	progressMarker := int(math.Ceil(float64(len(accounts)) / 10.0))
	start := time.Now()
	log.Infof("Updating limits for %d accounts", len(accounts))
	for i, account := range accounts {
		if (i+1)%progressMarker == 0 {
			log.Infof("Updated limits for %d accounts", i+1)
		}
		account := account
		workerPool.Go(func() *Result {
			taskStart := time.Now()
			resp := domain.UpdateLimits(cs, account)
			return &Result{
				Success:  resp,
				Duration: time.Since(taskStart).Seconds(),
			}
		})
	}
	res := workerPool.Wait()
	log.Infof("Updated limits for %d accounts in %.2f seconds", len(accounts), time.Since(start).Seconds())
	return res
}

func createNetwork(workerPool *pool.ResultPool[*Result], cs *cloudstack.CloudStackClient, parentDomainId string) []*Result {
	log.Infof("Fetching subdomains for domain %s", parentDomainId)
	domains := domain.ListSubDomains(cs, parentDomainId)

	progressMarker := int(math.Ceil(float64(len(domains)) / 10.0))
	start := time.Now()
	log.Infof("Creating %d networks", len(domains))
	for i, dmn := range domains {
		if (i+1)%progressMarker == 0 {
			log.Infof("Created %d networks", i+1)
		}
		i := i
		dmn := dmn
		workerPool.Go(func() *Result {
			taskStart := time.Now()
			_, err := network.CreateNetwork(cs, dmn.Id, i)
			if err != nil {
				return &Result{
					Success:  false,
					Duration: time.Since(taskStart).Seconds(),
				}
			}
			return &Result{
				Success:  true,
				Duration: time.Since(taskStart).Seconds(),
			}
		})
	}
	res := workerPool.Wait()
	log.Infof("Created %d networks in %.2f seconds", len(domains), time.Since(start).Seconds())
	return res
}

func createVms(workerPool *pool.ResultPool[*Result], cs *cloudstack.CloudStackClient, parentDomainId string, numVmPerNetwork int) []*Result {
	log.Infof("Fetching subdomains & accounts for domain %s", parentDomainId)
	domains := domain.ListSubDomains(cs, parentDomainId)
	var accounts []*cloudstack.Account
	for i := 0; i < len(domains); i++ {
		account := domain.ListAccounts(cs, domains[i].Id)
		accounts = append(accounts, account...)
	}

	domainIdAccountMapping := make(map[string]*cloudstack.Account)
	for _, account := range accounts {
		domainIdAccountMapping[account.Domainid] = account
	}

	log.Infof("Fetching networks for subdomains in domain %s", parentDomainId)
	var allNetworks []*cloudstack.Network
	for _, domain := range domains {
		network, _ := network.ListNetworks(cs, domain.Id)
		allNetworks = append(allNetworks, network...)
	}

	progressMarker := int(math.Ceil(float64(len(allNetworks)*numVmPerNetwork) / 10.0))
	start := time.Now()
	log.Infof("Creating %d VMs", len(allNetworks)*numVmPerNetwork)
	for i, network := range allNetworks {
		network := network
		for j := 1; j <= numVmPerNetwork; j++ {

			if (i*j+j)%progressMarker == 0 {
				log.Infof("Created %d VMs", i*j+j)
			}
			workerPool.Go(func() *Result {
				taskStart := time.Now()
				_, err := vm.DeployVm(cs, network.Domainid, network.Id, domainIdAccountMapping[network.Domainid].Name)
				if err != nil {
					return &Result{
						Success:  false,
						Duration: time.Since(taskStart).Seconds(),
					}
				}
				return &Result{
					Success:  true,
					Duration: time.Since(taskStart).Seconds(),
				}
			})
		}
	}
	res := workerPool.Wait()
	log.Infof("Created %d VMs in %.2f seconds", len(allNetworks)*numVmPerNetwork, time.Since(start).Seconds())
	return res
}

func createVolumes(workerPool *pool.ResultPool[*Result], cs *cloudstack.CloudStackClient, parentDomainId string, numVolumesPerVM int) []*Result {
	log.Infof("Fetching all VMs in subdomains for domain %s", parentDomainId)
	domains := domain.ListSubDomains(cs, parentDomainId)
	var allVMs []*cloudstack.VirtualMachine
	for _, dmn := range domains {
		vms, err := vm.ListVMs(cs, dmn.Id)
		if err != nil {
			log.Warn("Error listing VMs: ", err)
			continue
		}
		allVMs = append(allVMs, vms...)
	}

	progressMarker := int(math.Ceil(float64(len(allVMs)*numVolumesPerVM) / 10.0))
	start := time.Now()

	log.Infof("Creating %d volumes", len(allVMs)*numVolumesPerVM)
	unsuitableVmCount := 0

	for i, vm := range allVMs {
		vm := vm
		if vm.State != "Running" && vm.State != "Stopped" {
			unsuitableVmCount++
			continue
		}
		for j := 1; j <= numVolumesPerVM; j++ {
			if (i*j+j)%progressMarker == 0 {
				log.Infof("Created %d volumes", i*j+j)
			}

			workerPool.Go(func() *Result {
				taskStart := time.Now()
				vol, err := volume.CreateVolume(cs, vm.Domainid, vm.Account)
				if err != nil {
					return &Result{
						Success:  false,
						Duration: time.Since(taskStart).Seconds(),
					}
				}
				_, err = volume.AttachVolume(cs, vol.Id, vm.Id)
				if err != nil {
					return &Result{
						Success:  false,
						Duration: time.Since(taskStart).Seconds(),
					}
				}
				return &Result{
					Success:  true,
					Duration: time.Since(taskStart).Seconds(),
				}
			})
		}
	}
	if unsuitableVmCount > 0 {
		log.Warnf("Found %d VMs in unsuitable state", unsuitableVmCount)
	}
	res := workerPool.Wait()
	log.Infof("Created %d volumes in %.2f seconds", (len(allVMs)-unsuitableVmCount)*numVolumesPerVM, time.Since(start).Seconds())
	return res
}

func tearDownEnv() {
	parentDomain := config.ParentDomainId
	apiURL := config.URL

	for _, profile := range profiles {
		userProfileName := profile.Name
		if userProfileName == "admin" {
			cs := cloudstack.NewAsyncClient(apiURL, profile.ApiKey, profile.SecretKey, false)
			domains := domain.ListSubDomains(cs, parentDomain)
			log.Infof("Deleting %d domains", len(domains))
			for _, subdomain := range domains {
				domain.DeleteDomain(cs, subdomain.Id)
			}
			break
		}
	}
}
