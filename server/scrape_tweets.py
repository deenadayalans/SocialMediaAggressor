import sys
import json
import snscrape.modules.twitter as sntwitter

def scrape_tweets(keyword, max_results=10):
    results = []
    for i, tweet in enumerate(sntwitter.TwitterSearchScraper(f'{keyword}').get_items()):
        if i >= max_results:
            break
        results.append({
            'content': tweet.content,
            'url': f"https://twitter.com/{tweet.user.username}/status/{tweet.id}"
        })
    return results

if __name__ == "__main__":
    keyword = sys.argv[1]
    tweets = scrape_tweets(keyword)
    print(json.dumps(tweets))
