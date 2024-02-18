# Run

```bash
go run . -connections 2 -proxies etc/INSTANCES.txt -requests etc/LINKS.txt -log logs/"$(date -Ins).log"
```

# Test Coverage

```bash
go test -coverprofile cover.out && go tool cover -html cover.out
```
