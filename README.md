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
