package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "net/url"
    "os"
    "sort"
    "strings"
    "sync"
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
    newsSources          = []string{
        "https://news.google.com/rss/search?q=%s&hl=en-US&gl=US&ceid=US:en", // Google News with keyword
        "https://www.theguardian.com/world/rss",                            // The Guardian
        "https://www.aljazeera.com/xml/rss/all.xml",                        // Al Jazeera
        "https://feeds.bbci.co.uk/news/rss.xml",                            // BBC News
        "https://www.npr.org/rss/rss.php?id=1001",                          // NPR News
        "https://rss.nytimes.com/services/xml/rss/nyt/HomePage.xml",        // The New York Times
        "https://feeds.skynews.com/feeds/rss/home.xml",                     // Sky News
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

        // Use the first 5 handles as keywords (or randomly select)
        keyword = strings.Join(handles[:5], " OR ") // Combine handles with "OR" for broader search
    }

    var results = make(map[string][]FeedResult)
    var wg sync.WaitGroup
    var mu sync.Mutex

    // Fetch news feeds
    wg.Add(1)
    go func() {
        defer wg.Done()
        newsResults := fetchNewsFeeds(keyword)
        mu.Lock()
        results["News"] = newsResults
        mu.Unlock()
    }()

    // Fetch Twitter feeds
    wg.Add(1)
    go func() {
        defer wg.Done()
        twitterResults := fetchTwitterFeeds(keyword)
        mu.Lock()
        results["Twitter"] = twitterResults
        mu.Unlock()
    }()

    // Fetch YouTube feeds
    wg.Add(1)
    go func() {
        defer wg.Done()
        youtubeResults := fetchYouTubeFeeds(keyword)
        mu.Lock()
        results["YouTube"] = youtubeResults
        mu.Unlock()
    }()

    // Fetch Instagram feeds
    wg.Add(1)
    go func() {
        defer wg.Done()
        instagramResults := fetchInstagramFeeds(keyword)
        mu.Lock()
        results["Instagram"] = instagramResults
        mu.Unlock()
    }()

    // Fetch Facebook feeds
    wg.Add(1)
    go func() {
        defer wg.Done()
        facebookResults := fetchFacebookFeeds(keyword)
        mu.Lock()
        results["Facebook"] = facebookResults
        mu.Unlock()
    }()

    wg.Wait()
    return results
}

func fetchNewsFeeds(keyword string) []FeedResult {
    var results []FeedResult
    fp := gofeed.NewParser()

    for _, source := range newsSources {
        var urlStr string
        if strings.Contains(source, "%s") {
            // Format the URL with the keyword if it has a placeholder
            urlStr = fmt.Sprintf(source, url.QueryEscape(keyword))
        } else {
            // Use the URL as-is if it doesn't require a keyword
            urlStr = source
        }

        log.Printf("Fetching news feed from URL: %s", urlStr)

        feed, err := fp.ParseURL(urlStr)
        if err != nil {
            log.Printf("Error fetching news feed: %s", err)
            continue
        }

        for _, item := range feed.Items {
            // Filter results based on keyword
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

    // Sort results by published date (most recent first)
    sort.Slice(results, func(i, j int) bool {
        return results[i].Published.After(results[j].Published)
    })

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