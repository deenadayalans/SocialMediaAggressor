import socket
from flask import Flask, render_template, request
import feedparser
import requests
from bs4 import BeautifulSoup
from urllib.parse import quote_plus
from flask_caching import Cache
import time
from googleapiclient.discovery import build
import logging
import tweepy
import json
import os
import asyncio
import aiohttp
from datetime import datetime

app = Flask(__name__)
cache = Cache(app, config={'CACHE_TYPE': 'simple'})

# Configuration
USER_AGENT = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
REQUEST_TIMEOUT = 15

# Load handles from the configuration file
def load_handles():
    try:
        with open("twitterhandles.json", "r") as file:
            data = json.load(file)
            return data.get("handles", [])
    except Exception as e:
        print(f"Error loading handles: {e}")
        return []

# Example usage
handles = load_handles()

def find_free_port():
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(('0.0.0.0', 0))
        return s.getsockname()[1]

def fetch_with_timeout(url, timeout=REQUEST_TIMEOUT, verify_ssl=True):
    try:
        response = requests.get(
            url,
            headers={'User-Agent': USER_AGENT},
            timeout=timeout,
            verify=verify_ssl  # Allow disabling SSL verification
        )
        response.raise_for_status()
        return response.content
    except requests.exceptions.SSLError as e:
        print(f"SSL error fetching {url}: {str(e)}")
        return None
    except Exception as e:
        print(f"Error fetching {url}: {str(e)}")
        return None

async def fetch_with_aiohttp(url, session, timeout=REQUEST_TIMEOUT):
    """Fetch a URL asynchronously using aiohttp with SSL verification disabled."""
    try:
        async with session.get(url, timeout=timeout, ssl=False) as response:  # Disable SSL verification
            response.raise_for_status()
            return await response.text()
    except Exception as e:
        print(f"Error fetching {url}: {e}")
        return None

async def fetch_all_news(queries):
    """Fetch news results asynchronously for all queries."""
    results = []
    async with aiohttp.ClientSession(headers={'User-Agent': USER_AGENT}) as session:
        tasks = []
        for query in queries:
            encoded_query = quote_plus(query)
            for source_name, url_template in NEWS_SOURCES:
                url = url_template.format(encoded_query)
                tasks.append(fetch_with_aiohttp(url, session))
        responses = await asyncio.gather(*tasks, return_exceptions=True)
        for response in responses:
            if response:
                feed = feedparser.parse(response)
                for entry in feed.entries[:5]:
                    # Parse the published date into a datetime object
                    try:
                        published_date = datetime.strptime(entry.published, '%Y-%m-%dT%H:%M:%SZ')
                    except (AttributeError, ValueError):
                        published_date = datetime.min  # Use a default value if parsing fails

                    results.append({
                        'title': entry.title,
                        'link': entry.link,
                        'published': published_date,  # Store as datetime object
                        'description': BeautifulSoup(entry.description, 'html.parser').get_text() if hasattr(entry, 'description') else '',
                        'thumbnail': "https://via.placeholder.com/150",  # Use placeholder
                        'source': source_name
                    })
    return results

def count_results(results_dict):
    return sum(len(v) for v in results_dict.values())

# Twitter API Configuration
API_KEY = "OZIioj4j6s1F5wpMb58fMVy13"
API_SECRET = "vjhFty12BWFX5cVe7csmVeUMWlAAuA8OrUq71JDyh9AAMHBtbL"
BEARER_TOKEN = "AAAAAAAAAAAAAAAAAAAAAJ9p0gEAAAAAKXYGWatu0RR5QIuFj6iZ1S4HbTw%3D0Yv70zSBk3AucCguGd3KREhn3r0BTdZ88yAlPZXSyUZJghSUB9"

client = tweepy.Client(bearer_token=BEARER_TOKEN)

def split_keywords(keyword):
    """Split the keyword into the full phrase and individual words."""
    full_phrase = keyword.strip()
    individual_words = [word.strip() for word in full_phrase.split(",")]
    return full_phrase, individual_words

@cache.memoize(timeout=3600)  # Cache timeout set to 1 hour
def get_cached_results(query, source):
    """Retrieve cached results for a specific query and source."""
    cache_key = f"{source}:{query}"
    return cache.get(cache_key) or []

def cache_results(query, source, results):
    """Cache results for a specific query and source."""
    cache_key = f"{source}:{query}"
    cache.set(cache_key, results)

def merge_results(cached_results, new_results):
    """Merge cached and new results, removing duplicates and sorting by recency."""
    combined = {result['link']: result for result in cached_results + new_results}
    return sorted(combined.values(), key=lambda x: x['published'], reverse=True)

@cache.memoize(timeout=3600)
def get_twitter_api_results(keyword):
    try:
        full_phrase, individual_words = split_keywords(keyword)
        handles = load_handles()

        # Build queries for the full phrase and individual words
        queries = [full_phrase] + individual_words
        tweets = []

        for query in queries:
            # Retrieve cached results
            cached_tweets = get_cached_results(query, "Twitter")

            # Fetch new results
            handle_filters = " OR ".join([f"from:{handle}" for handle in handles])
            combined_query = f"{query} ({handle_filters})"

            try:
                response = client.search_recent_tweets(
                    query=combined_query,
                    tweet_fields=["created_at", "text", "author_id"],
                    max_results=50
                )
            except tweepy.TooManyRequests as e:
                print(f"Twitter API rate limit exceeded: {e}")
                return cached_tweets  # Return cached results if available

            new_tweets = []
            if response.data:
                for tweet in response.data:
                    new_tweets.append({
                        'title': f"Tweet by User {tweet.author_id}",
                        'link': f"https://twitter.com/user/status/{tweet.id}",
                        'published': tweet.created_at,
                        'description': tweet.text,
                        'source': 'Twitter'
                    })

            # Combine cached and new results
            combined_tweets = merge_results(cached_tweets, new_tweets)

            # Cache the combined results
            cache_results(query, "Twitter", combined_tweets)

            # Add to the final list of tweets
            tweets.extend(combined_tweets)

        # Sort tweets by the `published` timestamp in descending order
        tweets.sort(key=lambda x: x['published'], reverse=True)
        return tweets
    except Exception as e:
        print(f"Twitter API error: {e}")
        return []

@cache.memoize(timeout=3600)
def get_facebook_rss(keyword):
    full_phrase, individual_words = split_keywords(keyword)
    queries = [full_phrase] + individual_words
    results = []

    for query in queries:
        # Retrieve cached results
        cached_facebook = get_cached_results(query, "Facebook")

        # Fetch new results
        new_results = [{
            'title': f"Facebook post about {query}",
            'link': "https://facebook.com",
            'published': time.strftime('%Y-%m-%d'),
            'description': f"Sample Facebook content for {query}",
            'source': 'Facebook'
        }]

        # Combine cached and new results
        combined_facebook = merge_results(cached_facebook, new_results)

        # Cache the combined results
        cache_results(query, "Facebook", combined_facebook)

        # Add to the final list of results
        results.extend(combined_facebook)

    return results

@cache.memoize(timeout=3600)
def get_instagram_rss(keyword):
    full_phrase, individual_words = split_keywords(keyword)
    queries = [full_phrase] + individual_words
    results = []

    for query in queries:
        # Retrieve cached results
        cached_instagram = get_cached_results(query, "Instagram")

        # Fetch new results
        new_results = [{
            'title': f"Instagram post about {query}",
            'link': "https://instagram.com",
            'published': time.strftime('%Y-%m-%d'),
            'description': f"Sample Instagram content for {query}",
            'source': 'Instagram'
        }]

        # Combine cached and new results
        combined_instagram = merge_results(cached_instagram, new_results)

        # Cache the combined results
        cache_results(query, "Instagram", combined_instagram)

        # Add to the final list of results
        results.extend(combined_instagram)

    return results

@cache.memoize(timeout=3600)
def get_youtube_rss_with_api(keyword):
    full_phrase, individual_words = split_keywords(keyword)
    queries = [full_phrase] + individual_words
    videos = []

    for query in queries:
        # Retrieve cached results
        cached_youtube = get_cached_results(query, "YouTube")

        # Fetch new results
        new_results = []
        api_key = "AIzaSyBkb9hqvpvLV3uEGJ64n_NYeOCw9JSztCQ"
        youtube = build('youtube', 'v3', developerKey=api_key, cache_discovery=False)

        request = youtube.search().list(
            q=query,
            part='snippet',
            type='video',
            maxResults=10
        )
        response = request.execute()

        for item in response['items']:
            if 'videoId' in item['id']:
                new_results.append({
                    'title': item['snippet']['title'],
                    'link': f"https://www.youtube.com/watch?v={item['id']['videoId']}",
                    'published': item['snippet']['publishedAt'],
                    'description': item['snippet']['description'],
                    'thumbnail': item['snippet']['thumbnails']['default']['url'],  # Add thumbnail URL
                    'source': 'YouTube'
                })

        # Combine cached and new results
        combined_youtube = merge_results(cached_youtube, new_results)

        # Cache the combined results
        cache_results(query, "YouTube", combined_youtube)

        # Add to the final list of results
        videos.extend(combined_youtube)

    # Sort all results by the `published` timestamp in descending order
    videos.sort(key=lambda x: x['published'], reverse=True)
    return videos

@cache.memoize(timeout=3600)
def get_news_rss(keyword):
    full_phrase, individual_words = split_keywords(keyword)
    queries = [full_phrase] + individual_words

    # Use asyncio to fetch news results in parallel
    loop = asyncio.new_event_loop()
    asyncio.set_event_loop(loop)
    results = loop.run_until_complete(fetch_all_news(queries))

    # Sort results by the `published` timestamp in descending order
    results.sort(key=lambda x: x['published'], reverse=True)
    return results

NEWS_SOURCES = [
    ("Google News", "https://news.google.com/rss/search?q={}&hl=en-US&gl=US&ceid=US:en"),
    ("BBC News", "https://news.google.com/rss/search?q={}+source:BBC&hl=en-US&gl=US&ceid=US:en"),
    ("The Guardian", "https://www.theguardian.com/world/rss"),
    ("Al Jazeera", "https://www.aljazeera.com/xml/rss/all.xml")
]

# Global list to store searched keywords
searched_keywords = []

KEYWORDS_FILE = "searched_keywords.json"

def load_searched_keywords():
    """Load searched keywords and their counts from a JSON file."""
    if os.path.exists(KEYWORDS_FILE):
        try:
            with open(KEYWORDS_FILE, "r") as file:
                return json.load(file)
        except Exception as e:
            print(f"Error loading searched keywords: {e}")
    # Return an empty dictionary if the file doesn't exist
    return {}

def save_searched_keywords(keywords):
    """Save searched keywords and their counts to a JSON file."""
    try:
        # Ensure the file is created if it doesn't exist
        with open(KEYWORDS_FILE, "w") as file:
            json.dump(keywords, file)
        print(f"Keywords saved successfully to {KEYWORDS_FILE}")
    except Exception as e:
        print(f"Error saving searched keywords: {e}")

@app.route('/', methods=['GET', 'POST'])
def index():
    global searched_keywords
    keyword = request.form.get('keyword', '').strip()
    
    # Load searched keywords from the JSON file
    searched_keywords = load_searched_keywords()

    # Initialize with empty lists
    results = {
        'Twitter': [],
        'Facebook': [],
        'Instagram': [],
        'YouTube': [],
        'News': []
    }
    
    if keyword:
        # Increment the search count for the keyword
        if keyword in searched_keywords:
            searched_keywords[keyword] += 1
        else:
            searched_keywords[keyword] = 1

        # Save the updated keywords to the JSON file
        save_searched_keywords(searched_keywords)

        start_time = time.time()
        try:
            results['Twitter'] = get_twitter_api_results(keyword) or []
            results['Facebook'] = get_facebook_rss(keyword) or []
            results['Instagram'] = get_instagram_rss(keyword) or []
            results['YouTube'] = get_youtube_rss_with_api(keyword) or []
            results['News'] = get_news_rss(keyword) or []
        except Exception as e:
            print(f"Error fetching results: {e}")
        load_time = round(time.time() - start_time, 2)
    else:
        load_time = 0

    # Sort keywords by their search counts in descending order
    sorted_keywords = sorted(searched_keywords.items(), key=lambda x: x[1], reverse=True)

    total_results = count_results(results)
    
    return render_template('index.html',
                           keyword=keyword,
                           results=results,
                           load_time=load_time,
                           total_results=total_results,
                           searched_keywords=[kw[0] for kw in sorted_keywords])

if __name__ == '__main__':
    port = find_free_port()
    print(f"Running on http://localhost:{port}")
    app.run(debug=True, port=port, use_reloader=False)