package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "net/url"
    "sort"
    "strings"
    "sync"
	"os"
    "time"

    "github.com/dghubble/go-twitter/twitter"
    "github.com/gin-gonic/gin"
    "github.com/mmcdole/gofeed"
    "google.golang.org/api/youtube/v3"
    "google.golang.org/api/option"
)

type FeedResult struct {
    Title       string    `json:"title"`
    Link        string    `json:"link"`
    Published   time.Time `json:"published"`
    Description string    `json:"description"`
    Source      string    `json:"source"`
    Thumbnail   string    `json:"thumbnail"`
}

var (
    searchedKeywords     = make(map[string]int)
    searchedKeywordsLock sync.Mutex
    NEWS_SOURCES         = []string{
        "https://feeds.bbci.co.uk/news/rss.xml",                            // BBC News
        "https://rss.nytimes.com/services/xml/rss/nyt/HomePage.xml",        // The New York Times
        "https://feeds.skynews.com/feeds/rss/home.xml",                     // Sky News
        "https://www.theguardian.com/world/rss",                            // The Guardian
        "https://www.aljazeera.com/xml/rss/all.xml",                        // Al Jazeera
        "https://www.npr.org/rss/rss.php?id=1001",                          // NPR News
        "https://news.google.com/rss/search?q=%s&hl=en-US&gl=US&ceid=US:en", // Google News (dynamic)
    }
)

func main() {
    // Load searched keywords from file
    loadSearchedKeywords()

    // Set up Gin router
    r := gin.Default()

    // Serve static files
    r.Static("/static", "./static") // Map the "/static" URL path to the "./static" directory

    // Load HTML templates
    r.LoadHTMLGlob("templates/*")

    // Routes
    r.GET("/", indexHandler)
    r.POST("/search", searchHandler)

    // Start the server
    port := 8080
    fmt.Printf("Running on http://localhost:%d\n", port)
    r.Run(fmt.Sprintf(":%d", port))
}

func indexHandler(c *gin.Context) {
    // Sort keywords by search count
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

    // Increment search count
    searchedKeywordsLock.Lock()
    searchedKeywords[keyword]++
    saveSearchedKeywords()
    searchedKeywordsLock.Unlock()

    // Fetch results concurrently
    results := fetchAllFeeds(keyword)

    c.HTML(http.StatusOK, "index.html", gin.H{
        "keyword":          keyword,
        "results":          results,
        "searchedKeywords": sortKeywordsByCount(searchedKeywords),
    })
}

func fetchAllFeeds(keyword string) map[string][]FeedResult {
	if keyword == "" {
        // Load handles from twitterhandles.json
        handles := loadTwitterHandles()
        keyword = strings.Join(handles[:5], " OR ") // Combine handles with "OR" for broader search
    }

    var results = make(map[string][]FeedResult)
    var wg sync.WaitGroup
    var mu sync.Mutex

    // Fetch news from News API
    wg.Add(1)
    go func() {
        defer wg.Done()
        newsAPIResults := fetchNewsFeeds(keyword)
        log.Printf("Fetched %d results from News API", len(newsAPIResults))
        mu.Lock()
        results["NewsAPI"] = newsAPIResults
        mu.Unlock()
    }()

    // Fetch news from RSS feeds
    wg.Add(1)
    go func() {
        defer wg.Done()
        rssResults := fetchRSSFeeds(keyword)
        log.Printf("Fetched %d results from RSS feeds", len(rssResults))
        mu.Lock()
        results["RSS"] = rssResults
        mu.Unlock()
    }()

    // Fetch Twitter feeds
    wg.Add(1)
    go func() {
        defer wg.Done()
        twitterResults := fetchTwitterFeeds(keyword)
        log.Printf("Fetched %d results from Twitter", len(twitterResults))
        mu.Lock()
        results["Twitter"] = twitterResults
        mu.Unlock()
    }()

    // Fetch YouTube feeds
    wg.Add(1)
    go func() {
        defer wg.Done()
        youtubeResults := fetchYouTubeFeeds(keyword)
        log.Printf("Fetched %d results from YouTube", len(youtubeResults))
        mu.Lock()
        results["YouTube"] = youtubeResults
        mu.Unlock()
    }()

    // Fetch Instagram feeds
    wg.Add(1)
    go func() {
        defer wg.Done()
        instagramResults := fetchInstagramFeeds(keyword)
        log.Printf("Fetched %d results from Instagram", len(instagramResults))
        mu.Lock()
        results["Instagram"] = instagramResults
        mu.Unlock()
    }()

    // Fetch Facebook feeds
    wg.Add(1)
    go func() {
        defer wg.Done()
        facebookResults := fetchFacebookFeeds(keyword)
        log.Printf("Fetched %d results from Facebook", len(facebookResults))
        mu.Lock()
        results["Facebook"] = facebookResults
        mu.Unlock()
    }()

    // Wait for all goroutines to finish
    wg.Wait()

    // Combine News API and RSS results
    var combinedNewsResults []FeedResult
    combinedNewsResults = append(combinedNewsResults, results["NewsAPI"]...)
    combinedNewsResults = append(combinedNewsResults, results["RSS"]...)

    log.Printf("Total combined news results: %d", len(combinedNewsResults))

    // Add combined news results to the results map
    results["News"] = combinedNewsResults

    return results
}

func fetchNewsFeeds(keyword string) []FeedResult {
    apiKey := "7936e3ce6974483f9a64c8fb002229c4"
    if apiKey == "" {
        log.Println("Error: NEWS_API_KEY environment variable is not set")
        return nil
    }

    // Build the News API URL
    baseURL := "https://newsapi.org/v2/everything"
    query := url.QueryEscape(keyword)
    urlStr := fmt.Sprintf("%s?q=%s&language=en&sortBy=publishedAt&apiKey=%s", baseURL, query, apiKey)

    log.Printf("Fetching news feed from URL: %s", urlStr)

    // Make the HTTP request
    resp, err := http.Get(urlStr)
    if err != nil {
        log.Printf("Error fetching news feed: %s", err)
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
            Title:       article.Title,
            Link:        article.URL,
            Published:   published,
            Description: article.Description,
            Source:      article.Source.Name,
            Thumbnail:   article.URLToImage,
        })
    }

    // Sort results by published date (most recent first)
    sort.Slice(results, func(i, j int) bool {
        return results[i].Published.After(results[j].Published)
    })

    return results
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
                    Title:       item.Title,
                    Link:        item.Link,
                    Published:   published,
                    Description: item.Description,
                    Source:      feed.Title,
                    Thumbnail:   "https://via.placeholder.com/150", // Placeholder thumbnail
                })
            }
        }
    }

    log.Printf("Processed %d articles from RSS feeds", len(results))
    return results
}

func fetchTwitterFeeds(keyword string) []FeedResult {
    bearerToken := "AAAAAAAAAAAAAAAAAAAAAJ9p0gEAAAAAKXYGWatu0RR5QIuFj6iZ1S4HbTw%3D0Yv70zSBk3AucCguGd3KREhn3r0BTdZ88yAlPZXSyUZJghSUB9"

    // Create a custom HTTP client with the bearer token
    httpClient := &http.Client{
        Transport: &transportWithBearerToken{
            BearerToken: bearerToken,
            Base:        http.DefaultTransport,
        },
    }

    // Create a Twitter client
    client := twitter.NewClient(httpClient)

    // Search for tweets
    search, _, err := client.Search.Tweets(&twitter.SearchTweetParams{
        Query: keyword,
        Count: 10,
    })
    if err != nil {
        log.Printf("Error fetching Twitter feeds: %s", err)
        return nil
    }

    // Process the results
    var results []FeedResult
    for _, tweet := range search.Statuses {
        published, err := time.Parse(time.RubyDate, tweet.CreatedAt)
        if err != nil {
            log.Printf("Error parsing tweet timestamp: %s", err)
            published = time.Now() // Use current time as fallback
        }

        results = append(results, FeedResult{
            Title:       fmt.Sprintf("Tweet by @%s", tweet.User.ScreenName),
            Link:        fmt.Sprintf("https://twitter.com/%s/status/%s", tweet.User.ScreenName, tweet.IDStr),
            Published:   published,
            Description: tweet.Text,
            Source:      "Twitter",
            Thumbnail:   tweet.User.ProfileImageURL,
        })
    }
    return results
}

func fetchYouTubeFeeds(keyword string) []FeedResult {
    apiKey := "AIzaSyBkb9hqvpvLV3uEGJ64n_NYeOCw9JSztCQ"

    // Create a YouTube service with the API key
    service, err := youtube.NewService(context.Background(), option.WithAPIKey(apiKey))
    if err != nil {
        log.Printf("Error creating YouTube service: %s", err)
        return nil
    }

    // Make the API call
    call := service.Search.List([]string{"id", "snippet"}).
        Q(keyword).
        Type("video").
        MaxResults(10)

    response, err := call.Do()
    if err != nil {
        log.Printf("Error fetching YouTube feeds: %s", err)
        return nil
    }

    // Process the results
    var results []FeedResult
    for _, item := range response.Items {
        published, _ := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
        results = append(results, FeedResult{
            Title:       item.Snippet.Title,
            Link:        fmt.Sprintf("https://www.youtube.com/watch?v=%s", item.Id.VideoId),
            Published:   published,
            Description: item.Snippet.Description,
            Source:      "YouTube",
            Thumbnail:   item.Snippet.Thumbnails.Default.Url,
        })
    }

    return results
}

func fetchInstagramFeeds(keyword string) []FeedResult {
    // Placeholder for Instagram API integration
    return []FeedResult{
        {
            Title:       fmt.Sprintf("Instagram post about %s", keyword),
            Link:        "https://instagram.com",
            Published:   time.Now(),
            Description: fmt.Sprintf("Sample Instagram content for %s", keyword),
            Source:      "Instagram",
            Thumbnail:   "https://via.placeholder.com/150",
        },
    }
}

func fetchFacebookFeeds(keyword string) []FeedResult {
    // Placeholder for Facebook API integration
    return []FeedResult{
        {
            Title:       fmt.Sprintf("Facebook post about %s", keyword),
            Link:        "https://facebook.com",
            Published:   time.Now(),
            Description: fmt.Sprintf("Sample Facebook content for %s", keyword),
            Source:      "Facebook",
            Thumbnail:   "https://via.placeholder.com/150",
        },
    }
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
    if err := encoder.Encode(searchedKeywords); err != nil {
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

type transportWithBearerToken struct {
    BearerToken string
    Base        http.RoundTripper
}

func (t *transportWithBearerToken) RoundTrip(req *http.Request) (*http.Response, error) {
    req.Header.Set("Authorization", "Bearer "+t.BearerToken)
    return t.Base.RoundTrip(req)
}