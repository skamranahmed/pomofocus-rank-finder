package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const pomofocusHost = "pomofocus.io"
const pomofocusRankingApiPath = "/ranking-hours"

func main() {
	log.SetFlags(0)

	pomofocusUserID := flag.String("userId", "", "user id of your pomofocus account")
	approach := flag.String("approach", "sequential", "approach for searching")
	numOfWorkers := flag.Int("workerCount", 5, "number of concurrent workers")

	flag.Parse()

	if *pomofocusUserID == "" {
		log.Printf("❗️User ID must be provided.")
		return
	}

	if *approach == "sequential" {
		fmt.Println("Performing sequential search for the userID:", *pomofocusUserID)
		sequentialSearch(*pomofocusUserID)
		return
	}

	if *approach == "concurrent" {
		if *numOfWorkers <= 0 {
			log.Printf("❗️Worker count must be greater than zero.")
			return
		}

		fmt.Printf("Performing concurrent search for the userID: %s using %d concurrent worker.\n", *pomofocusUserID, *numOfWorkers)
		concurrentSearch(*pomofocusUserID, *numOfWorkers)
		return
	}
}

func concurrentSearch(userID string, numberOfWorkers int) {
	rankingApiBaseUrl := getPomofocusRankingApiBaseUrl()
	dateDigitListQueryParams := buildDateDigitListQueryParams()

	numOfWorkers := numberOfWorkers
	rankPageValuesChan := make(chan int, numOfWorkers)
	rankPageResultsChan := make(chan int, numOfWorkers)

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	for i := 0; i < numOfWorkers; i++ {
		workerNum := i + 1

		go func(ctx context.Context, contextCancelFunc context.CancelFunc, workerNumber int, rankPageValuesReadOnlyChannel <-chan int, rankPageResultsWriteOnlyChannel chan<- int) {
			for rankPageValue := range rankPageValuesReadOnlyChannel {
				select {
				case <-ctx.Done():
					// Although this line will never get printed because when the `rankPageResultsWriteOnlyChannel` channel is being read
					// an early return is being done as soon as the rank page value is found that is not -1.
					fmt.Printf("Early stopping worker %d because the rank page is found by some other worker.\n", workerNumber)
					rankPageResultsWriteOnlyChannel <- -1
					return
				default:
					fmt.Printf("⏳ Worker %d is processing rank page %d.\n", workerNumber, rankPageValue)
				}

				rankData, err := getRankData(rankingApiBaseUrl, dateDigitListQueryParams, rankPageValue)
				if err != nil {
					// Doing a `.Fatal` in a go routine will also make the main go routine to exit.
					// But this is okay for the use case because if any of the rank page is not fetched, the program must exit.
					log.Fatal(err)
				}

				if len(rankData) == 0 {
					// The rank page returns empty result.
					// Send -1 to the rankPageResultWriteOnlyChannel.
					// Exit the current worker only, but let other workers continue their processing.
					fmt.Printf("✅ Worker %d reached the rank page with empty data. No more work to be done by this worker.\n", workerNumber)
					rankPageResultsWriteOnlyChannel <- -1
					return
				}

				for _, dataItem := range rankData {
					if dataItem.ID == userID {
						// The rank page contains the user id.
						// Send the rank page value to the rankPageResultWriteOnlyChannel.
						// Also signal other workers to stop their work.
						fmt.Printf("✨✅ Worker %d found the user id in rank page %d. Signaling other workers to also stop their work.\n", workerNumber, rankPageValue)
						rankPageResultsWriteOnlyChannel <- rankPageValue
						contextCancelFunc()
						return
					}
				}

				fmt.Printf("❌ Worker %d did not find the user id in rank page %d\n", workerNumber, rankPageValue)
			}
		}(ctx, cancelFunc, workerNum, rankPageValuesChan, rankPageResultsChan)

	}

	go func(rankPageValuesWriteOnlyChan chan<- int) {
		rankPage := 0
		for {
			rankPageValuesWriteOnlyChan <- rankPage
			rankPage += 1
		}
	}(rankPageValuesChan)

	for i := 0; i < numOfWorkers; i++ {
		resultValue := <-rankPageResultsChan
		if resultValue != -1 {
			fmt.Println("🙌 UserID found in rank page:", resultValue)
			return
		}
	}

	fmt.Println("❗️UserID was not found in any of the rank page.")
	return
}

func sequentialSearch(userID string) {
	rankingApiBaseUrl := getPomofocusRankingApiBaseUrl()
	dateDigitListQueryParams := buildDateDigitListQueryParams()
	rankPage := 0

	for {
		rankData, err := getRankData(rankingApiBaseUrl, dateDigitListQueryParams, rankPage)
		if err != nil {
			log.Fatal(err)
		}

		if len(rankData) == 0 {
			fmt.Println("❗️UserID was not found in any of the rank page.")
			return
		}

		for _, item := range rankData {
			if item.ID == userID {
				fmt.Println("🙌 UserID found in rank page:", rankPage)
				return
			}
		}

		fmt.Println("❌ UserID not found in page:", rankPage)
		rankPage += 1
	}
}

func getRankData(rankingApiBaseUrl url.URL, dateDigitListQueryParams string, rankPage int) ([]PomofocusRankResponse, error) {
	fullApiUrl := buildFullUrl(rankingApiBaseUrl, dateDigitListQueryParams, rankPage)

	response, err := http.Get(fullApiUrl)
	if err != nil {
		return nil, fmt.Errorf("request to endpoint failed, error: %+v\n", err)
	}
	defer response.Body.Close()

	var responseObject []PomofocusRankResponse
	err = json.NewDecoder(response.Body).Decode(&responseObject)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JSON: %v\n", err)
	}

	return responseObject, nil
}

func getPomofocusRankingApiBaseUrl() url.URL {
	return url.URL{
		Scheme: "https",
		Host:   pomofocusHost,
		Path:   pomofocusRankingApiPath,
	}
}

func buildDateDigitListQueryParams() string {
	today := time.Now().UTC()

	var sb strings.Builder

	for i := 0; i <= 6; i++ {
		if i > 0 {
			sb.WriteString("&")
		}
		updatedDay := today.AddDate(0, 0, -i)
		dateDigitListQueryParamsString := fmt.Sprintf("dateDigitList[]=%04d%02d%02d", updatedDay.Year(), int(updatedDay.Month()), updatedDay.Day())
		sb.WriteString(dateDigitListQueryParamsString)
	}

	return sb.String()
}

func buildFullUrl(baseUrl url.URL, dateDigitListQueryParams string, rankPage int) string {
	baseUrl.RawQuery = dateDigitListQueryParams + buildRankPageQueryParam(rankPage)
	return baseUrl.String()
}

func buildRankPageQueryParam(rankPage int) string {
	return fmt.Sprintf("&rankPage=%d", rankPage)
}

type PomofocusRankResponse struct {
	ID          string `json:"_id"`
	TimeFocused string `json:"timeFocused"`
	Name        string `json:"name"`
	Picture     string `json:"picture"`
}
