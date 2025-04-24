package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

const antiCaptchaAPIKey = "your-anti-captcha-api-key"

func randomSleep(min, max int) {
	time.Sleep(time.Duration(rand.Intn(max-min)+min) * time.Millisecond)
}

func solveCaptcha(captchaImage []byte) (string, error) {
	// Step 1: Create a task
	task := map[string]interface{}{
		"clientKey": antiCaptchaAPIKey,
		"task": map[string]interface{}{
			"type":      "ImageToTextTask",
			"body":      base64.StdEncoding.EncodeToString(captchaImage),
			"phrase":    false,
			"case":      false,
			"numeric":   false,
			"math":      false,
			"minLength": 0,
			"maxLength": 0,
		},
	}

	taskData, err := json.Marshal(task)
	if err != nil {
		return "", err
	}

	resp, err := http.Post("https://api.anti-captcha.com/createTask", "application/json", bytes.NewReader(taskData))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var taskResponse struct {
		ErrorID int    `json:"errorId"`
		TaskID  int    `json:"taskId"`
		Error   string `json:"errorDescription"`
	}
	err = json.Unmarshal(body, &taskResponse)
	if err != nil {
		return "", err
	}

	if taskResponse.ErrorID != 0 {
		return "", fmt.Errorf("Anti-Captcha error: %s", taskResponse.Error)
	}

	taskID := taskResponse.TaskID

	// Step 2: Wait for the solution
	for {
		time.Sleep(5 * time.Second) // Wait for 5 seconds before checking the result

		result := map[string]interface{}{
			"clientKey": antiCaptchaAPIKey,
			"taskId":    taskID,
		}

		resultData, err := json.Marshal(result)
		if err != nil {
			return "", err
		}

		resp, err := http.Post("https://api.anti-captcha.com/getTaskResult", "application/json", bytes.NewReader(resultData))
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}

		var resultResponse struct {
			ErrorID  int    `json:"errorId"`
			Status   string `json:"status"`
			Solution struct {
				Text string `json:"text"`
			} `json:"solution"`
			Error string `json:"errorDescription"`
		}
		err = json.Unmarshal(body, &resultResponse)
		if err != nil {
			return "", err
		}

		if resultResponse.ErrorID != 0 {
			return "", fmt.Errorf("Anti-Captcha error: %s", resultResponse.Error)
		}

		if resultResponse.Status == "ready" {
			return resultResponse.Solution.Text, nil
		}
	}
}

func main() {
	// Define Facebook credentials (use environment variables or a secure method in production)
	email := "deenadayalan_s@hotmail.com"
	password := "Sana@31518"

	// Define the keyword to search for
	keyword := "technology"

	// Construct the Facebook public search URL
	pageURL := "https://www.facebook.com/public/" + keyword

	// Create a context for Chromedp
	ctx, cancel := chromedp.NewExecAllocator(context.Background(), append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false), // Disable headless mode for debugging
		chromedp.Flag("disable-gpu", false),
	)...)
	defer cancel()

	ctx, cancel = chromedp.NewContext(ctx)
	defer cancel()

	var htmlContent string
	var captchaImage []byte

	// Step 1: Log in to Facebook
	log.Println("Navigating to Facebook login page...")
	err := chromedp.Run(ctx,
		chromedp.Navigate("https://www.facebook.com/"),
	)
	if err != nil {
		log.Fatalf("Error navigating to Facebook login page: %s", err)
	}

	randomSleep(1000, 3000) // Random delay

	log.Println("Entering email...")
	err = chromedp.Run(ctx, chromedp.SendKeys(`#email`, email, chromedp.ByID))
	if err != nil {
		log.Fatalf("Error entering email: %s", err)
	}

	randomSleep(1000, 3000) // Random delay

	log.Println("Entering password...")
	err = chromedp.Run(ctx, chromedp.SendKeys(`#pass`, password, chromedp.ByID))
	if err != nil {
		log.Fatalf("Error entering password: %s", err)
	}

	randomSleep(1000, 3000) // Random delay

	log.Println("Clicking login button...")
	err = chromedp.Run(ctx, chromedp.Click(`button[name="login"]`, chromedp.ByQuery))
	if err != nil {
		log.Fatalf("Error clicking login button: %s", err)
	}

	// Step 2: Check for CAPTCHA
	log.Println("Checking for CAPTCHA...")
	err = chromedp.Run(ctx,
		chromedp.Screenshot(`#captcha-image-selector`, &captchaImage), // Replace with the actual CAPTCHA image selector
	)
	if err == nil {
		log.Println("CAPTCHA detected. Attempting to solve...")

		// Solve the CAPTCHA
		captchaSolution, err := solveCaptcha(captchaImage)
		if err != nil {
			log.Fatalf("Error solving CAPTCHA: %s", err)
		}

		log.Printf("CAPTCHA solution: %s", captchaSolution)

		// Enter the CAPTCHA solution
		err = chromedp.Run(ctx,
			chromedp.SendKeys(`#captcha-input-selector`, captchaSolution, chromedp.ByID), // Replace with the actual CAPTCHA input selector
			chromedp.Click(`#captcha-submit-button`, chromedp.ByID),                      // Replace with the actual CAPTCHA submit button selector
		)
		if err != nil {
			log.Fatalf("Error submitting CAPTCHA solution: %s", err)
		}

		log.Println("CAPTCHA solved successfully!")
	} else {
		log.Println("No CAPTCHA detected. Proceeding...")
	}

	// Step 3: Wait for feed to load
	log.Println("Waiting for feed to load...")
	err = chromedp.Run(ctx, chromedp.WaitVisible(`div[role="feed"]`, chromedp.ByQuery))
	if err != nil {
		log.Fatalf("Error waiting for feed to load: %s", err)
	}
	log.Println("Successfully logged into Facebook.")

	// Step 4: Navigate to the Facebook public search page
	log.Printf("Navigating to Facebook public search page: %s", pageURL)
	err = chromedp.Run(ctx,
		chromedp.Navigate(pageURL),    // Navigate to the Facebook public search page
		chromedp.Sleep(5*time.Second), // Wait for the page to load
	)
	if err != nil {
		log.Fatalf("Error navigating to Facebook: %s", err)
	}

	// Step 5: Extract the HTML content of the page
	log.Println("Attempting to extract HTML content...")
	err = chromedp.Run(ctx, chromedp.OuterHTML("body", &htmlContent))
	if err != nil {
		log.Fatalf("Error extracting HTML content: %s", err)
	}

	// Step 6: Parse the HTML content using goquery
	log.Println("Parsing the extracted HTML content...")
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		log.Fatalf("Error parsing HTML content: %s", err)
	}

	// Step 7: Extract posts from the parsed HTML
	var results []string
	doc.Find(`div[role="article"]`).Each(func(i int, s *goquery.Selection) {
		postContent := strings.TrimSpace(s.Text())
		postLink, exists := s.Find("a").Attr("href")
		if exists && strings.Contains(postLink, "/posts/") {
			fullLink := "https://www.facebook.com" + postLink
			results = append(results, postContent+" ("+fullLink+")")
		}
	})

	// Step 8: Log the extracted posts
	log.Printf("Extracted %d posts from Facebook search results.", len(results))
	for i, post := range results {
		log.Printf("Post %d: %s", i+1, post)
	}
}
