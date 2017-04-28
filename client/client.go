package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ShowMax/go-fqdn"
	"github.com/prometheus/common/log"

	"gitlab.com/robust-perception/tug_of_war/util"
)

var (
	myFqdn   = flag.String("fqdn", fqdn.Get(), "FQDN to register with")
	proxyUrl = flag.String("proxy-url", "http://pushprox.robustperception.io:8080", "Push proxy to talk to.")
)

func doScrape(request *http.Request, client *http.Client) {
	logger := log.With("scrape_id", request.Header.Get("id"))
	ctx, _ := context.WithTimeout(request.Context(), util.GetScrapeTimeout(request.Header))
	request = request.WithContext(ctx)

	scrapeResp, err := client.Do(request)
	if err != nil {
		msg := fmt.Sprintf("Failed to scrape %s: %s", request.URL.String(), err)
		log.Warn(msg)
		resp := &http.Response{
			StatusCode: 500,
			Header:     http.Header{},
			Body:       ioutil.NopCloser(strings.NewReader(msg)),
		}
		err = doPush(resp, request, client)
		if err != nil {
			log.Warnf("Failed to push failed scrape response: %s", err)
			return
		}
		log.Info("Pushed failed scrape response")
		return
	}
	logger.Info("Retrieved scrape response")

	err = doPush(scrapeResp, request, client)
	if err != nil {
		logger.Warnf("Failed to push scrape response: %s", err)
		return
	}
	logger.Info("Pushed scrape result")
}

// Report the result of the scrape back up to the proxy.
func doPush(resp *http.Response, origRequest *http.Request, client *http.Client) error {
	resp.Header.Set("id", origRequest.Header.Get("id")) // Link the request and response
	// Remaining scrape deadline.
	deadline, _ := origRequest.Context().Deadline()
	resp.Header.Set("X-Prometheus-Scrape-Timeout", fmt.Sprintf("%f", float64(time.Until(deadline))/1e9))

	u, _ := url.Parse(*proxyUrl + "/push")

	buf := &bytes.Buffer{}
	resp.Write(buf)
	request := &http.Request{
		Method:        "POST",
		URL:           u,
		Body:          ioutil.NopCloser(buf),
		ContentLength: int64(buf.Len()),
	}
	request = request.WithContext(origRequest.Context())
	_, err := client.Do(request)
	if err != nil {
		return err
	}
	return nil
}

func loop() {
	client := &http.Client{}
	resp, err := client.Post(*proxyUrl+"/poll", "", strings.NewReader(*myFqdn))
	if err != nil {
		log.Infof("Error polling: %s", err)
		time.Sleep(time.Second) // Don't pound the server.
		return
	}
	defer resp.Body.Close()
	request, _ := http.ReadRequest(bufio.NewReader(resp.Body))
	log.With("scrape_id", request.Header.Get("id")).With("url", request.URL).Info("Got scrape request")
	request.RequestURI = ""

	go doScrape(request, client)
}

func main() {
	flag.Parse()
	log.Infof("Using FQDN of %s", *myFqdn)
	for {
		loop()
	}
}