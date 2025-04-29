package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
	"github.com/mmcdole/gofeed"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

type CrawlRequest struct {
	Keyword string `json:"keyword"`
}

type CrawlResponse struct {
	Results []string `json:"results"`
}

type FeedResult struct {
	Title         string `json:"title"`
	Link          string `json:"link"`
	Published     string `json:"published"`
	PublishedTime time.Time
	Description   string `json:"description"`
	Source        string `json:"source"`
	Thumbnail     string `json:"thumbnail"`
}

var NEWS_SOURCES []string
var newsCache sync.Map // Cache for news feeds

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get current working directory: %s", err)
	}
	log.Printf("Current working directory: %s", cwd)

	NEWS_SOURCES, err = loadNewsSources("server/news_sources.json")
	if err != nil {
		log.Fatalf("Failed to load news sources: %s", err)
	}

	http.HandleFunc("/crawl/facebook", facebookCrawlHandler)
	http.HandleFunc("/crawl/twitter", twitterCrawlHandler)
	http.HandleFunc("/crawl/youtube", youtubeCrawlHandler)
	http.HandleFunc("/crawl/news", newsCrawlHandler)
	http.HandleFunc("/crawl/news/pagination", newsPaginationHandler)
	http.HandleFunc("/news", newsHandler)
	http.HandleFunc("/social", socialHandler)
	http.HandleFunc("/", indexHandler)

	port := 8081
	log.Printf("Crawler server running on http://localhost:%d", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

func facebookCrawlHandler(w http.ResponseWriter, r *http.Request) {
	handleCrawl(w, r, func(req CrawlRequest) []string {
		ctx, cancel := chromedp.NewContext(context.Background())
		defer cancel()

		log.Printf("Starting Facebook crawl for keyword: %s", req.Keyword)

		// Log in to Facebook
		log.Println("Attempting to log in to Facebook...")
		start := time.Now()
		err := chromedp.Run(ctx,
			chromedp.Navigate("https://www.facebook.com/login"),
			chromedp.SendKeys(`#email`, "deenadayalan_s@hotmail.com", chromedp.ByID),
			chromedp.SendKeys(`#pass`, "Shivam@13522", chromedp.ByID),
			chromedp.Click(`button[name="login"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`div[role="feed"]`, chromedp.ByQuery),
		)
		log.Printf("Facebook login took %s", time.Since(start))
		if err != nil {
			log.Printf("Error logging into Facebook: %s", err)
			return []string{"Error: Unable to log into Facebook"}
		}
		log.Println("Successfully logged into Facebook.")

		// Search for the keyword
		var htmlContent string
		pageURL := "https://www.facebook.com/public/" + url.QueryEscape(req.Keyword)
		log.Printf("Navigating to Facebook search page: %s", pageURL)
		start = time.Now()
		err = chromedp.Run(ctx,
			chromedp.Navigate(pageURL),
			chromedp.WaitVisible(`div[role="article"]`, chromedp.ByQuery),
			chromedp.OuterHTML("body", &htmlContent),
		)
		log.Printf("Facebook search page navigation took %s", time.Since(start))
		if err != nil {
			log.Printf("Error crawling Facebook search page: %s", err)
			return []string{"Error: Unable to fetch Facebook posts"}
		}
		log.Println("Successfully fetched HTML content from Facebook search page.")

		// Parse the HTML content
		var results []string
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err != nil {
			log.Printf("Error parsing Facebook HTML: %s", err)
			return []string{"Error: Unable to parse Facebook posts"}
		}
		log.Println("Successfully parsed Facebook HTML content.")

		doc.Find(`div[role="article"]`).Each(func(i int, s *goquery.Selection) {
			postContent := strings.TrimSpace(s.Text())
			postLink, exists := s.Find("a").Attr("href")
			if exists && strings.Contains(postLink, "/posts/") {
				fullLink := "https://www.facebook.com" + postLink
				results = append(results, fmt.Sprintf("%s (%s)", postContent, fullLink))
			}
		})
		log.Printf("Fetched %d Facebook posts.", len(results))

		return results
	})
}

func twitterCrawlHandler(w http.ResponseWriter, r *http.Request) {
	handleCrawl(w, r, func(req CrawlRequest) []string {
		ctx, cancel := chromedp.NewContext(context.Background())
		defer cancel()

		var htmlContent string
		pageURL := "https://twitter.com/search?q=" + url.QueryEscape(req.Keyword)

		err := chromedp.Run(ctx,
			chromedp.Navigate(pageURL),
			chromedp.OuterHTML("body", &htmlContent),
		)
		if err != nil {
			log.Printf("Error crawling Twitter: %s", err)
			return nil
		}

		var results []string
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err != nil {
			log.Printf("Error parsing Twitter HTML: %s", err)
			return nil
		}

		doc.Find("div[data-testid='tweet']").Each(func(i int, s *goquery.Selection) {
			tweetContent := strings.TrimSpace(s.Text())
			tweetLink, exists := s.Find("a").Attr("href")
			if exists && strings.Contains(tweetLink, "/status/") {
				fullLink := "https://twitter.com" + tweetLink
				results = append(results, fmt.Sprintf("%s (%s)", tweetContent, fullLink))
			}
		})

		return results
	})
}

func youtubeCrawlHandler(w http.ResponseWriter, r *http.Request) {
	handleCrawl(w, r, func(req CrawlRequest) []string {
		apiKey := "AIzaSyBkb9hqvpvLV3uEGJ64n_NYeOCw9JSztCQ" // Set your YouTube Data API key as an environment variable
		if apiKey == "" {
			log.Println("Error: YOUTUBE_API_KEY environment variable is not set")
			return nil
		}

		service, err := youtube.NewService(r.Context(), option.WithAPIKey(apiKey))
		if err != nil {
			log.Printf("Error creating YouTube service: %s", err)
			return nil
		}

		call := service.Search.List([]string{"id", "snippet"}).
			Q(req.Keyword).
			Type("video").
			MaxResults(10)

		start := time.Now()
		response, err := call.Do()
		log.Printf("YouTube API call took %s", time.Since(start))
		if err != nil {
			log.Printf("Error fetching YouTube results: %s", err)
			return nil
		}

		var results []string
		for _, item := range response.Items {
			videoTitle := item.Snippet.Title
			videoLink := fmt.Sprintf("https://www.youtube.com/watch?v=%s", item.Id.VideoId)
			videoThumbnail := item.Snippet.Thumbnails.Default.Url // Fetch the thumbnail URL
			results = append(results, fmt.Sprintf("%s (%s) [Thumbnail: %s]", videoTitle, videoLink, videoThumbnail))
		}

		return results
	})
}

func newsCrawlHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the keyword from the request
	keyword := r.URL.Query().Get("keyword")
	if keyword == "" {
		http.Error(w, "Keyword is required", http.StatusBadRequest)
		return
	}

	// Fetch combined news feeds
	results := fetchCombinedNewsFeeds(keyword)

	// Return the results as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(results); err != nil {
		log.Printf("Error encoding response: %s", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func newsPaginationHandler(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")
	pageStr := r.URL.Query().Get("page")
	if keyword == "" {
		http.Error(w, "Keyword is required", http.StatusBadRequest)
		return
	}

	// Default to page 1 if no page is provided
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil {
			page = p
		}
	}

	results := fetchNewsFeedsWithPagination(keyword, page)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(results); err != nil {
		log.Printf("Error encoding response: %s", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func fetchCombinedNewsFeeds(keyword string) []FeedResult {
	var allResults []FeedResult
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Fetch results from RSS feeds
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		rssResults := fetchRSSFeeds(keyword)
		log.Printf("Fetching RSS feeds took %s", time.Since(start))
		mu.Lock()
		allResults = append(allResults, rssResults...)
		mu.Unlock()
	}()

	// Fetch results from News API
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		newsAPIResults := fetchNewsFeeds(keyword)
		log.Printf("Fetching News API results took %s", time.Since(start))
		mu.Lock()
		allResults = append(allResults, newsAPIResults...)
		mu.Unlock()
	}()

	wg.Wait()

	// Sort all results by recency
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].PublishedTime.After(allResults[j].PublishedTime)
	})

	// Limit to the most recent 100 results
	if len(allResults) > 100 {
		allResults = allResults[:100]
	}

	return allResults
}

func fetchRSSFeeds(keyword string) []FeedResult {
	var results []FeedResult
	fp := gofeed.NewParser()

	for _, source := range NEWS_SOURCES {
		var urlStr string
		if strings.Contains(source, "%s") {
			// Format the URL with the keyword if it has a placeholder
			urlStr = fmt.Sprintf(source, url.QueryEscape(keyword))
		} else {
			// Use the URL as-is if it doesn't require a keyword
			urlStr = source
		}

		log.Printf("Fetching RSS feed from URL: %s", urlStr)

		feed, err := fp.ParseURL(urlStr)
		if err != nil {
			log.Printf("Error fetching RSS feed: %s", err)
			continue
		}

		log.Printf("Fetched %d items from RSS feed: %s", len(feed.Items), source)

		for _, item := range feed.Items {
			// Filter articles by keyword
			if strings.Contains(strings.ToLower(item.Title), strings.ToLower(keyword)) ||
				strings.Contains(strings.ToLower(item.Description), strings.ToLower(keyword)) {
				published, _ := time.Parse(time.RFC1123Z, item.Published)
				results = append(results, FeedResult{
					Title:         item.Title,
					Link:          item.Link,
					Published:     published.Format("2006-01-02 15:04:05"),
					PublishedTime: published,
					Description:   item.Description,
					Source:        feed.Title,
					Thumbnail:     "https://via.placeholder.com/150", // Placeholder thumbnail
				})
			}
		}
	}

	log.Printf("Processed %d articles from RSS feeds", len(results))
	return results
}

func fetchNewsFeeds(keyword string) []FeedResult {
	apiKey := "7936e3ce6974483f9a64c8fb002229c4" // Replace with your actual API key
	if apiKey == "" {
		log.Println("Error: NEWS_API_KEY environment variable is not set")
		return nil
	}

	// Build the News API URL
	baseURL := "https://newsapi.org/v2/everything"
	query := url.QueryEscape(keyword)
	urlStr := fmt.Sprintf("%s?q=%s&language=en&sortBy=publishedAt&apiKey=%s", baseURL, query, apiKey)

	log.Printf("Fetching news feed from URL: %s", urlStr)

	client := &http.Client{
		Timeout: 10 * time.Second, // Set a 10-second timeout
	}
	resp, err := client.Get(urlStr)
	if err != nil {
		log.Printf("Error fetching URL: %s", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Error: News API returned status code %d", resp.StatusCode)
		return nil
	}

	// Parse the response
	var apiResponse struct {
		Articles []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			URL         string `json:"url"`
			PublishedAt string `json:"publishedAt"`
			Source      struct {
				Name string `json:"name"`
			} `json:"source"`
			URLToImage string `json:"urlToImage"`
		} `json:"articles"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		log.Printf("Error decoding News API response: %s", err)
		return nil
	}

	log.Printf("News API returned %d articles", len(apiResponse.Articles))

	// Process the results
	var results []FeedResult
	for _, article := range apiResponse.Articles {
		published, _ := time.Parse(time.RFC3339, article.PublishedAt)
		results = append(results, FeedResult{
			Title:         article.Title,
			Link:          article.URL,
			Published:     published.Format("2006-01-02 15:04:05"),
			PublishedTime: published,
			Description:   article.Description,
			Source:        article.Source.Name,
			Thumbnail:     article.URLToImage,
		})
	}

	// Sort results by published date (most recent first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].PublishedTime.After(results[j].PublishedTime)
	})

	return results
}

func fetchNewsFeedsWithPagination(keyword string, page int) []FeedResult {
	apiKey := "7936e3ce6974483f9a64c8fb002229c4" // Replace with your actual News API key
	if apiKey == "" {
		log.Println("Error: NEWS_API_KEY environment variable is not set")
		return nil
	}

	// Build the News API URL with pagination
	baseURL := "https://newsapi.org/v2/everything"
	query := url.QueryEscape(keyword)
	urlStr := fmt.Sprintf("%s?q=%s&language=en&sortBy=publishedAt&page=%d&apiKey=%s", baseURL, query, page, apiKey)

	log.Printf("Fetching paginated news feed from URL: %s", urlStr)

	client := &http.Client{
		Timeout: 10 * time.Second, // Set a 10-second timeout
	}
	resp, err := client.Get(urlStr)
	if err != nil {
		log.Printf("Error fetching URL: %s", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Error: News API returned status code %d", resp.StatusCode)
		return nil
	}

	// Parse the response
	var apiResponse struct {
		Articles []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			URL         string `json:"url"`
			PublishedAt string `json:"publishedAt"`
			Source      struct {
				Name string `json:"name"`
			} `json:"source"`
			URLToImage string `json:"urlToImage"`
		} `json:"articles"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		log.Printf("Error decoding paginated News API response: %s", err)
		return nil
	}

	log.Printf("News API returned %d articles for page %d", len(apiResponse.Articles), page)

	// Process the results
	var results []FeedResult
	for _, article := range apiResponse.Articles {
		published, _ := time.Parse(time.RFC3339, article.PublishedAt)
		log.Printf("Article Thumbnail: %s", article.URLToImage)
		results = append(results, FeedResult{
			Title:         article.Title,
			Link:          article.URL,
			Published:     published.Format("2006-01-02 15:04:05"),
			PublishedTime: published,
			Description:   article.Description,
			Source:        article.Source.Name,
			Thumbnail:     article.URLToImage,
		})
	}

	// Sort results by published date (most recent first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].PublishedTime.After(results[j].PublishedTime)
	})

	return results
}

func handleCrawl(w http.ResponseWriter, r *http.Request, crawlFunc func(CrawlRequest) []string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var req CrawlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Invalid request body: %s", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Add a timeout for the crawl function
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second) // Increased timeout to 45 seconds
	defer cancel()

	resultsChan := make(chan []string, 1)
	go func() {
		resultsChan <- crawlFunc(req)
	}()

	var results []string
	select {
	case results = <-resultsChan:
		// Successfully fetched results
	case <-ctx.Done():
		log.Printf("Crawl function timed out for keyword: %s", req.Keyword)
		if len(results) > 0 {
			log.Printf("Returning partial results for keyword: %s", req.Keyword)
			break // Return partial results
		}
		http.Error(w, "Crawl function timed out", http.StatusGatewayTimeout)
		return
	}

	resp := CrawlResponse{Results: results}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func loadNewsSources(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("error opening news sources file: %w", err)
	}
	defer file.Close()

	var data struct {
		Sources []string `json:"sources"`
	}
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		return nil, fmt.Errorf("error decoding news sources file: %w", err)
	}

	return data.Sources, nil
}

func newsHandler(w http.ResponseWriter, r *http.Request) {
	// Fetch news feeds (reuse existing logic)
	results := fetchNewsFeedsWithCache()

	// Render the news.html template
	tmpl := template.Must(template.ParseFiles("client/templates/news.html"))
	err := tmpl.Execute(w, map[string]interface{}{
		"News": results, // Pass the results directly as "News"
	})
	if err != nil {
		log.Printf("Error rendering news template: %s", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func socialHandler(w http.ResponseWriter, r *http.Request) {
	// Fetch social feeds (reuse existing logic)
	results := fetchAllSocialFeeds()

	// Render the social.html template
	tmpl := template.Must(template.ParseFiles("client/templates/social.html"))
	err := tmpl.Execute(w, map[string]interface{}{
		"results": results,
	})
	if err != nil {
		log.Printf("Error rendering social template: %s", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func fetchNewsFeedsWithCache() []FeedResult {
	// Check if cached results exist
	if cached, ok := newsCache.Load("news"); ok {
		log.Println("Returning cached news feeds")
		return cached.([]FeedResult)
	}

	// Fetch fresh news feeds
	log.Println("Fetching fresh news feeds")
	results := fetchCombinedNewsFeeds("") // Pass an empty keyword for all news feeds

	// Cache the results
	newsCache.Store("news", results)
	log.Println("Cached fresh news feeds")

	return results
}

func fetchAllSocialFeeds() map[string][]FeedResult {
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make(map[string][]FeedResult)

	// Fetch Twitter feeds
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("Fetching Twitter feeds")
		twitterResults := fetchFromHandler(twitterCrawlHandler, "Twitter")
		mu.Lock()
		results["Twitter"] = twitterResults
		mu.Unlock()
	}()

	// Fetch Facebook feeds
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("Fetching Facebook feeds")
		facebookResults := fetchFromHandler(facebookCrawlHandler, "Facebook")
		mu.Lock()
		results["Facebook"] = facebookResults
		mu.Unlock()
	}()

	// // Fetch Instagram feeds
	// wg.Add(1)
	// go func() {
	// 	defer wg.Done()
	// 	log.Println("Fetching Instagram feeds")
	// 	instagramResults := fetchFromHandler(instagramCrawlHandler, "Instagram")
	// 	mu.Lock()
	// 	results["Instagram"] = instagramResults
	// 	mu.Unlock()
	// }()

	// Fetch YouTube feeds
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("Fetching YouTube feeds")
		youtubeResults := fetchFromHandler(youtubeCrawlHandler, "YouTube")
		mu.Lock()
		results["YouTube"] = youtubeResults
		mu.Unlock()
	}()

	wg.Wait()
	return results
}

func fetchYouTubeFeeds(keyword string) []FeedResult {
	apiKey := "YOUR_YOUTUBE_API_KEY" // Replace with your YouTube Data API key
	if apiKey == "" {
		log.Println("Error: YOUTUBE_API_KEY environment variable is not set")
		return nil
	}

	// Create a new YouTube service
	service, err := youtube.NewService(context.Background(), option.WithAPIKey(apiKey))
	if err != nil {
		log.Printf("Error creating YouTube service: %s", err)
		return nil
	}

	// Build the YouTube API search request
	call := service.Search.List([]string{"id", "snippet"}).
		Q(keyword).
		Type("video").
		MaxResults(10)

	// Execute the API call
	start := time.Now()
	response, err := call.Do()
	log.Printf("YouTube API call took %s", time.Since(start))
	if err != nil {
		log.Printf("Error fetching YouTube results: %s", err)
		return nil
	}

	// Process the API response
	var results []FeedResult
	for _, item := range response.Items {
		// Parse the published date
		published, err := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
		if err != nil {
			log.Printf("Error parsing published date for video %s: %s", item.Id.VideoId, err)
			published = time.Now() // Default to current time if parsing fails
		}

		// Append the video details to the results
		results = append(results, FeedResult{
			Title:         item.Snippet.Title,
			Link:          fmt.Sprintf("https://www.youtube.com/watch?v=%s", item.Id.VideoId),
			Published:     published.Format("2006-01-02 15:04:05"),
			PublishedTime: published,
			Description:   item.Snippet.Description,
			Source:        "YouTube",
			Thumbnail:     item.Snippet.Thumbnails.Default.Url, // Use the default thumbnail
		})
	}

	// Log the number of results fetched
	log.Printf("Fetched %d YouTube videos for keyword: %s", len(results), keyword)

	return results
}

func fetchFromHandler(handler func(http.ResponseWriter, *http.Request), platform string) []FeedResult {
	// Simulate an HTTP request and response
	reqBody := CrawlRequest{Keyword: ""}
	bodyBytes, _ := json.Marshal(reqBody)
	req := &http.Request{
		Method: http.MethodPost,
		Body:   io.NopCloser(bytes.NewReader(bodyBytes)), // Send a valid JSON body
		Header: http.Header{"Content-Type": []string{"application/json"}},
	}
	w := &mockResponseWriter{}

	// Call the handler
	handler(w, req)

	// Parse the response
	var crawlResponse CrawlResponse
	if err := json.Unmarshal([]byte(w.body.String()), &crawlResponse); err != nil {
		log.Printf("Error parsing %s handler response: %s", platform, err)
		return nil
	}

	// Convert to FeedResult format
	var results []FeedResult
	for _, result := range crawlResponse.Results {
		results = append(results, FeedResult{
			Title: result,
			Link:  "", // Add logic to extract links if needed
		})
	}

	return results
}

type mockResponseWriter struct {
	header http.Header
	body   strings.Builder
	status int
}

func (m *mockResponseWriter) Header() http.Header {
	if m.header == nil {
		m.header = make(http.Header)
	}
	return m.header
}

func (m *mockResponseWriter) Write(data []byte) (int, error) {
	return m.body.Write(data)
}

func (m *mockResponseWriter) WriteHeader(statusCode int) {
	m.status = statusCode
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	// Fetch both social and news feeds
	socialFeeds := fetchAllSocialFeeds()
	newsFeeds := fetchNewsFeedsWithCache()

	// Combine the results into a single response
	results := map[string]interface{}{
		"Social": socialFeeds,
		"News":   newsFeeds,
	}

	cwd, err := os.Getwd()
	if err != nil {
		log.Printf("Error getting current working directory: %s", err)
		return
	}
	tmplPath := fmt.Sprintf("%s/client/templates/index.html", cwd)

	// Parse and render the template
	tmpl := template.Must(template.ParseFiles(tmplPath))
	if err := tmpl.Execute(w, map[string]interface{}{"results": results}); err != nil {
		log.Printf("Error rendering index template: %s", err)
	}
}
