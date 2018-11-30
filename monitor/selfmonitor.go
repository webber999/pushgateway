// NOTE:
//     Add Feature, monitor pushgateway self metrics, if the metric's
// last push time is older than aging time, it will be deleted by pushgateway
// self, avoid prometheus get a down node server's metrics.
//
// Version: 0.6.0 github/prometheus/pushgateway

package monitor

import (
	"fmt"
	"github.com/prometheus/common/log"
	"io"
	"io/ioutil"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func SelfMonitorAgingTime(agingTime time.Duration, listenAddress, metricsPath, routePrefix string) {
	queryUrl := fmt.Sprintf("http://localhost%s%s%s", listenAddress, routePrefix, metricsPath)
	timeout := time.Second * 10

	log.Infof("Loop: self monitor aging time %s start", agingTime)
	time.Sleep(agingTime - time.Minute)
	for {
		// every minute check, waiting for next time
		time.Sleep(time.Minute)

		// make http requests
		allMetricsBody, err := fetchHTTP(queryUrl, timeout)()
		if err != nil {
			log.Errorf("fetchHTTP(%s) failed %s", queryUrl, err)
			allMetricsBody.Close()
			continue
		}

		allMetricData, err := ioutil.ReadAll(allMetricsBody)
		if err != nil {
			log.Errorf("ioutil.ReadAll(%s) failed %s", queryUrl, err)
			allMetricsBody.Close()
			continue
		}
		// can not use defer, because this goroutine will never return
		allMetricsBody.Close()

		resultString := string(allMetricData)
		// example: push_time_seconds{hostName="XXX",instance="localhost:9091",job="push_gateway"} 1.5411578179228673e+09
		re, err := regexp.Compile("push_time_seconds{.*\n")
		if err != nil {
			log.Warnf("There is no push data in pushgateway! %s", err)
			continue
		}
		hitStringList := re.FindAllString(resultString, -1)
		if hitStringList == nil {
			log.Warn("There is no push data in pushgateway!")
			continue
		}
		for _, onePushString := range hitStringList {
			timeStamp := strings.Split(onePushString, " ")[1]
			trimTimeStamp := strings.TrimSuffix(timeStamp, "\n")
			lastPushTime, _ := strconv.ParseFloat(trimTimeStamp, 64)
			if lastPushTime > float64(time.Now().Unix()-int64(agingTime/time.Second)) {
				continue
			} else {
				// call http API，delete Metrics
				metric := strings.Split(onePushString, " ")[0]
				ok := deleteMetrics(queryUrl, metric)
				if !ok {
					log.Errorf("delete metrics failed!! %s", onePushString)
					continue
				}
				log.Warnf("delete old metrics success!! %s", onePushString)
			}
		}
		time.Sleep(time.Millisecond * 50)
	}
}

func fetchHTTP(uri string, timeout time.Duration) func() (io.ReadCloser, error) {
	http.DefaultClient.Timeout = timeout

	return func() (io.ReadCloser, error) {
		resp, err := http.DefaultClient.Get(uri)
		if err != nil {
			return nil, err
		}
		if !(resp.StatusCode >= 200 && resp.StatusCode < 300) {
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP status %d", resp.StatusCode)
		}
		return resp.Body, nil
	}
}

func deleteMetrics(baseUrl, metrics string) bool {
	jobName := ""
	labelKV := ""
	//metrics := "push_time_seconds{hostName=\"XXX\",instance=\"localhost:9091\",job=\"push_gateway\"}"
	splitMetricsList := strings.Split(strings.TrimSuffix(strings.TrimPrefix(metrics, "push_time_seconds{"), "}"), ",")

	for _, label := range splitMetricsList {
		labelKey := strings.Split(label, "=")[0]
		labelValue := strings.Replace(strings.Split(label, "=")[1], "\"", "", -1)
		if labelKey == "job" {
			jobName = labelValue
		} else {
			labelKV += "/" + labelKey + "/" + labelValue
		}
	}

	deleteUrl := fmt.Sprintf("%s/job/%s%s", baseUrl, jobName, labelKV)
	deleteOK := deleteHTTP(deleteUrl)
	if !deleteOK {
		return false
	}
	return true
}

func deleteHTTP(deleteUrl string) bool {
	timeout := time.Duration(time.Second * 3)
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest("DELETE", deleteUrl, nil)
	if err != nil {
		log.Error(err)
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Error(err)
		return false
	} else if resp.StatusCode != 202 {
		log.Error("delete failed, get wrong response status code: ", resp.StatusCode)
		return false
	}
	defer resp.Body.Close()
	return true
}

