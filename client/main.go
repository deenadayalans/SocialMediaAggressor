package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type FeedResult struct {
	Title         string    `json:"title"`
	Link          string    `json:"link"`
	Published     string    `json:"published"`
	PublishedTime time.Time `json:"publishedTime"`
	Description   string    `json:"description"`
	Source        string    `json:"source"`
	Thumbnail     string    `json:"thumbnail"`
}

var (
	searchedKeywords     = make(map[string]int)
	searchedKeywordsLock sync.Mutex
	cache                = sync.Map{}
	twitterHandles       []string
)

func main() {
	// Load searched keywords and Twitter handles
	loadSearchedKeywords()
	twitterHandles = loadTwitterHandles()

	// Set up Gin router
	r := gin.Default()
	r.Static("/static", "./static")
	r.LoadHTMLGlob("templates/*")

	// Routes
	r.GET("/", indexHandler)
	r.POST("/search", searchHandler)
	r.GET("/news", newsPaginationHandler)

	// Start the server
	port := 8080
	fmt.Printf("Running on http://localhost:%d\n", port)
	r.Run(fmt.Sprintf(":%d", port))
}

func indexHandler(c *gin.Context) {
	searchedKeywordsLock.Lock()
	sortedKeywords := sortKeywordsByCount(searchedKeywords)
	searchedKeywordsLock.Unlock()

	c.HTML(http.StatusOK, "index.html", gin.H{
		"searchedKeywords": sortedKeywords,
		"keyword":          "",
	})
}

func searchHandler(c *gin.Context) {
	keyword := c.PostForm("keyword")
	if keyword == "" {
		c.Redirect(http.StatusFound, "/")
		return
	}

	searchedKeywordsLock.Lock()
	searchedKeywords[keyword]++
	saveSearchedKeywords()
	searchedKeywordsLock.Unlock()

	results := fetchAllFeeds(keyword)

	c.HTML(http.StatusOK, "index.html", gin.H{
		"keyword":          keyword,
		"results":          results,
		"searchedKeywords": sortKeywordsByCount(searchedKeywords),
	})
}

func newsPaginationHandler(c *gin.Context) {
	keyword := c.Query("keyword")
	page := c.DefaultQuery("page", "1")
	pageNum, _ := strconv.Atoi(page)

	results := fetchNewsFeedsWithPagination(keyword, pageNum)
	c.JSON(http.StatusOK, gin.H{"results": results})
}

func fetchAllFeeds(keyword string) map[string][]FeedResult {
	var results = make(map[string][]FeedResult)
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Fetch News feeds with caching
	wg.Add(1)
	go func() {
		defer wg.Done()
		newsResults := fetchNewsFeedsWithCache(keyword)
		mu.Lock()
		results["News"] = newsResults
		mu.Unlock()
	}()

	// Fetch YouTube feeds with caching
	wg.Add(1)
	go func() {
		defer wg.Done()
		youtubeResults := fetchYouTubeFeedsWithCache(keyword)
		mu.Lock()
		results["YouTube"] = youtubeResults
		mu.Unlock()
	}()

	// Fetch Twitter feeds with caching
	wg.Add(1)
	go func() {
		defer wg.Done()
		twitterResults := fetchFeedsFromServer("twitter", keyword)
		mu.Lock()
		results["Twitter"] = twitterResults
		mu.Unlock()
	}()

	// Fetch Facebook feeds
	wg.Add(1)
	go func() {
		defer wg.Done()
		facebookResults := fetchFeedsFromServer("facebook", keyword)
		mu.Lock()
		results["Facebook"] = facebookResults
		mu.Unlock()
	}()

	// Fetch Instagram feeds
	wg.Add(1)
	go func() {
		defer wg.Done()
		instagramResults := fetchFeedsFromServer("instagram", keyword)
		mu.Lock()
		results["Instagram"] = instagramResults
		mu.Unlock()
	}()

	// Wait for all goroutines to finish
	wg.Wait()
	return results
}

func fetchFeedsFromServer(platform, keyword string) []FeedResult {
	client := &http.Client{
		Timeout: 10 * time.Second, // Set a 10-second timeout
	}
	payload := map[string]string{
		"keyword": keyword,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error marshaling request payload for %s: %s", platform, err)
		return nil
	}

	url := fmt.Sprintf("http://localhost:8081/crawl/%s", platform)
	resp, err := client.Post(url, "application/json", bytes.NewReader(payloadBytes))
	if err != nil {
		log.Printf("Error sending request to %s server: %s", platform, err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Server returned status code %d for %s", resp.StatusCode, platform)
		return nil
	}

	var crawlResponse struct {
		Results []string `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&crawlResponse); err != nil {
		log.Printf("Error decoding response from %s server: %s", platform, err)
		return nil
	}

	var results []FeedResult
	for _, item := range crawlResponse.Results {
		// Extract the actual link, title, and thumbnail from the result string
		var link, title, thumbnail string
		if strings.Contains(item, "(") && strings.Contains(item, ")") {
			start := strings.LastIndex(item, "(")
			end := strings.LastIndex(item, ")")
			if start != -1 && end != -1 && start < end {
				link = item[start+1 : end]
				title = strings.TrimSpace(item[:start])
			}
		}
		if strings.Contains(item, "[Thumbnail: ") && strings.Contains(item, "]") {
			thumbStart := strings.LastIndex(item, "[Thumbnail: ") + len("[Thumbnail: ")
			thumbEnd := strings.LastIndex(item, "]")
			if thumbStart != -1 && thumbEnd != -1 && thumbStart < thumbEnd {
				thumbnail = item[thumbStart:thumbEnd]
			}
		}

		results = append(results, FeedResult{
			Title:         title,
			Description:   item,
			Source:        strings.Title(platform),
			Link:          link,
			Published:     time.Now().Format("2006-01-02 15:04:05"),
			PublishedTime: time.Now(),
			Thumbnail:     thumbnail,
		})
	}
	return results
}

func fetchNewsFeedsWithPagination(keyword string, page int) []FeedResult {
	serverURL := fmt.Sprintf("http://localhost:8081/crawl/news/pagination?keyword=%s&page=%d", url.QueryEscape(keyword), page)

	resp, err := http.Get(serverURL)
	if err != nil {
		log.Printf("Error fetching paginated news feeds from server: %s", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Server returned status code %d", resp.StatusCode)
		return nil
	}

	var results []FeedResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		log.Printf("Error decoding server response: %s", err)
		return nil
	}

	return results
}

func fetchNewsFeedsFromServer(keyword string) []FeedResult {
	serverURL := fmt.Sprintf("http://localhost:8081/crawl/news?keyword=%s", url.QueryEscape(keyword))

	resp, err := http.Get(serverURL)
	if err != nil {
		log.Printf("Error fetching news feeds from server: %s", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Server returned status code %d", resp.StatusCode)
		return nil
	}

	var results []FeedResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		log.Printf("Error decoding server response: %s", err)
		return nil
	}

	return results
}

func fetchNewsFeedsWithCache(keyword string) []FeedResult {
	// Check if the results are cached
	if cached, ok := cache.Load("news:" + keyword); ok {
		return cached.([]FeedResult)
	}

	// Fetch results from the server
	results := fetchNewsFeedsFromServer(keyword)

	// Cache the results
	cache.Store("news:"+keyword, results)

	return results
}

func fetchYouTubeFeedsWithCache(keyword string) []FeedResult {
	if cached, ok := cache.Load("youtube:" + keyword); ok {
		return cached.([]FeedResult)
	}

	results := fetchFeedsFromServer("youtube", keyword)
	cache.Store("youtube:"+keyword, results)
	return results
}

func loadSearchedKeywords() {
	file, err := os.Open("searched_keywords.json")
	if err != nil {
		log.Printf("No existing keywords file found: %s", err)
		return
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&searchedKeywords); err != nil {
		log.Printf("Error decoding keywords file: %s", err)
	}
}

func saveSearchedKeywords() {
	file, err := os.Create("searched_keywords.json")
	if err != nil {
		log.Printf("Error saving keywords file: %s", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	if err := encoder.Encode(&searchedKeywords); err != nil {
		log.Printf("Error encoding keywords file: %s", err)
	}
}

func loadTwitterHandles() []string {
	file, err := os.Open("twitterhandles.json")
	if err != nil {
		log.Fatalf("Error opening twitterhandles.json: %v", err)
	}
	defer file.Close()

	var data struct {
		Handles []string `json:"handles"`
	}
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		log.Fatalf("Error decoding twitterhandles.json: %v", err)
	}

	return data.Handles
}

func sortKeywordsByCount(keywords map[string]int) []string {
	type kv struct {
		Key   string
		Value int
	}
	var sorted []kv
	for k, v := range keywords {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Value > sorted[j].Value
	})
	var result []string
	for _, kv := range sorted {
		result = append(result, kv.Key)
	}
	return result
}
