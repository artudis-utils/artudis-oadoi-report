package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type Record struct {
	Publication
	APIResponses []APIResponse
}

type Publication struct {
	Identifier []struct {
		Scheme string `json:"scheme"`
		Value  string `json:"value"`
	} `json:"identifier"`
	ID         string `json:"__id__"`
	Type       string `json:"type"`
	Attachment []struct {
		OpenAccess  string      `json:"open_access"`
		BlobKey     string      `json:"blob_key"`
		ExternalURL interface{} `json:"external_url"`
		Type        string      `json:"type"`
	} `json:"attachment"`
}

type APIResponse struct {
	HTTPStatus string
	APIResponseBody
	JSONDecodeError string
	GETError        string
}

type APIResponseBody struct {
	BestOaLocation struct {
		Evidence          string `json:"evidence"`
		HostType          string `json:"host_type"`
		ID                string `json:"id"`
		URL               string `json:"url"`
		URLForLandingPage string `json:"url_for_landing_page"`
		URLForPdf         string `json:"url_for_pdf"`
		Version           string `json:"version"`
	} `json:"best_oa_location"`
	DataStandard int    `json:"data_standard"`
	Doi          string `json:"doi"`
	DoiURL       string `json:"doi_url"`
	IsOa         bool   `json:"is_oa"`
	JournalIsOa  bool   `json:"journal_is_oa"`
	JournalIssns string `json:"journal_issns"`
	JournalName  string `json:"journal_name"`
	Publisher    string `json:"publisher"`
	Title        string `json:"title"`
	Updated      string `json:"updated"`
	Year         int    `json:"year"`
}

const OADOIURL string = "https://api.oadoi.org/v2/"

var attachmentTypeToWeightMap = map[string]int{
	"missing":             0,
	"other":               1,
	"submittedManuscript": 2,
	"acceptedManuscript":  3,
	"finalVersion":        4,
}

var email = flag.String("email", "", "Email to pass to the oaDOI API")
var httplimit = flag.Int("httplimit", 5, "Number of HTTP requests that can run concurrently")

func findFilesToProcess() []string {
	if len(flag.Args()) == 0 {
		log.Println("No file names provided, trying to find files ending with Publication-export.json in current working directory.")
		workingDir, err := os.Getwd()
		if err != nil {
			log.Fatalln("Error getting working directory. ", err)
		}
		matches, err := filepath.Glob(filepath.Join(workingDir, "*Publication-export.json"))
		if err != nil {
			log.Fatalln("Error finding matching files. ", err)
		}
		return matches
	} else {
		return flag.Args()
	}
}

func processFile(fileName string, waitgroupFiles *sync.WaitGroup) {
	defer waitgroupFiles.Done()
	file, err := os.Open(fileName)
	if err != nil {
		log.Println(err)
		return
	}
	defer file.Close()

	output := make(chan Record)

	ticketToHTTP := make(chan bool, *httplimit)
	for i := 0; i < *httplimit; i++ {
		ticketToHTTP <- true
	}

	var waitgroupLines sync.WaitGroup
	fileScanner := bufio.NewScanner(file)
	for fileScanner.Scan() {
		waitgroupLines.Add(1)
		publicationBytes := append([]byte{}, fileScanner.Bytes()...)
		go processPublication(publicationBytes, &waitgroupLines, ticketToHTTP, output)
	}

	var waitgroupOutput sync.WaitGroup
	waitgroupOutput.Add(1)
	go processOutput(output, &waitgroupOutput)

	waitgroupLines.Wait()
	close(output)
	close(ticketToHTTP)
	waitgroupOutput.Wait()

}

func processOutput(output <-chan Record, waitgroupOutput *sync.WaitGroup) {
	defer waitgroupOutput.Done()

	w := csv.NewWriter(os.Stdout)

	header := []string{"API - Available OA",
		"Artudis - Available OA",
		"Artudis - Best Type OA",
		"Artudis - ID",
		"API - Best OA Location URL",
		"API - Best OA Location Version",
		"Artudis - Publication Type",
		"API - HTTP Response Status",
		"API - JSON Decode Error",
		"API - GET Error",
		"API - DOI",
		"API - Title",
	}

	err := w.Write(header)

	for record := range output {

		artudisOA := false
		highestLevel := "missing"
		for _, attachment := range record.Attachment {
			if attachment.OpenAccess == "true" {
				artudisOA = true
				if attachmentTypeToWeightMap[attachment.Type] > attachmentTypeToWeightMap[highestLevel] {
					highestLevel = attachment.Type
				}
			}
		}

		for _, apiresponse := range record.APIResponses {
			toCSVOutput := []string{strconv.FormatBool(apiresponse.APIResponseBody.IsOa),
				strconv.FormatBool(artudisOA),
				highestLevel,
				record.Publication.ID,
				apiresponse.APIResponseBody.BestOaLocation.URL,
				apiresponse.APIResponseBody.BestOaLocation.Version,
				record.Publication.Type,
				apiresponse.HTTPStatus,
				apiresponse.JSONDecodeError,
				apiresponse.GETError,
				apiresponse.APIResponseBody.Doi,
				apiresponse.APIResponseBody.Title,
			}

			err := w.Write(toCSVOutput)

			if err != nil {
				log.Println("error writing record to csv:", err)
				return
			}
		}
	}

	w.Flush()

	err = w.Error()
	if err != nil {
		log.Println("error writing record to csv:", err)
		return
	}
}

func processPublication(publicationBytes []byte, waitgroupLines *sync.WaitGroup, ticketToHTTP chan bool, output chan<- Record) {
	defer waitgroupLines.Done()

	var record Record

	err := json.Unmarshal(publicationBytes, &record.Publication)
	if err != nil {
		log.Println(err)
		return
	}

	for _, identifier := range record.Publication.Identifier {
		if identifier.Scheme == "doi" {
			record.APIResponses = append(record.APIResponses, doAPIRequest(identifier.Value, ticketToHTTP))
		}
	}

	output <- record
}

func doAPIRequest(doi string, ticketToHTTP chan bool) APIResponse {
	defer func() { ticketToHTTP <- true }()

	// Wait for ticket
	<-ticketToHTTP

	var apiResponse APIResponse

	url := OADOIURL + strings.TrimPrefix(doi, "http://dx.doi.org/") + "?email=" + *email
	resp, err := http.Get(url)
	if err != nil {
		apiResponse.GETError = err.Error()
		return apiResponse
	}

	defer resp.Body.Close()

	apiResponse.HTTPStatus = resp.Status

	err = json.NewDecoder(resp.Body).Decode(&apiResponse.APIResponseBody)
	if err != nil {
		apiResponse.JSONDecodeError = err.Error()
		return apiResponse
	}

	return apiResponse
}

func main() {
	flag.Parse()

	if *email == "" {
		log.Fatal("FATAL: An email is required.")
	}

	filesToProcess := findFilesToProcess()
	if len(filesToProcess) == 0 {
		log.Fatalln("Could not find any files to process.")
	}
	var waitgroupFiles sync.WaitGroup
	for _, fileName := range filesToProcess {
		waitgroupFiles.Add(1)
		log.Println("Processing", fileName)
		processFile(fileName, &waitgroupFiles)
	}
	waitgroupFiles.Wait()
}
