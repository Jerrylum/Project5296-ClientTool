# Introduction


Downloading files via HTTP GET requests is common, but server bandwidth limitations can hinder download speeds, especially when downloading multiple files simultaneously. Previous studies retrieve files from multiple mirror servers. Existing solutions use multi-threading to address this issue. However, they are still constrained by the maximum number of concurrent connections per IP.

This project aims to overcome these limitations by leveraging cloud computing technologies to enhance file download speeds, specifically Amazon Elastic Compute Cloud (EC2) instances. We proposed a cloud-based solution that employs a leader-follower model with EC2 instances acting as the HTTP proxies and follower nodes. By identifying and addressing bottlenecks such as server bandwidth, proxy throughput, and the maximum number of concurrent connections, an optimized network resources scheduler on the client is designed to distribute download requests to the proxy nodes and monitor the download progress, maximizing download speed and proxy node utilization by segmenting file downloads into parts and assigning them to different connections.

This repository is the implementation of the client side of our solution. It is responsible for downloading files from the Internet. It can be run with a list of download requests and a list of proxy servers to use. The client will then distribute the download requests to the proxy servers and monitor the download progress.

# Usage

```
Usage: go run . [options]
  -connections int
        The number of connections in total to download
  -log string
        The path to the log file. If not provided, the log will be discarded.
  -name string
        The name of the current execution. If not provided, the name will be 'default' (default "default")
  -proxies string
        The path to a file with a list of proxy server ips, separated by linefeed
  -requests string
        The path to a file with a list of download requests, separated by linefeed
        Each line can be one of the following formats:
         - Only the URL
           e.g. 'http://example.com/file.zip'
           The file will be saved to the current directory with the same name as the file in the url
         - URL with existing directory
           e.g. 'http://example.com/file.zip > /path/to/save/'
           The file will be saved to the specified existing directory with the same name as the file in the url 
         - URL with specified file path
           e.g. 'http://example.com/file.zip > /path/to/save/file.zip'
           The file will be saved to the specified path
    
  -timeLog string
        The path to the time log file. If not provided, the log will be discarded.
```

# Usage Example

```bash
go run . -connections 2 -proxies etc/INSTANCES.txt -requests etc/LINKS.txt -log logs/"$(date -Ins).log"
```

# Test Coverage

```bash
go test -coverprofile cover.out && go tool cover -html cover.out
```
