# Go Module Management

## Key Commands

- `go mod init <module>` — initialize a new module
- `go mod tidy` — add missing / remove unused dependencies
- `go get <pkg>@latest` — add or update a dependency
- `go get <pkg>@v1.2.3` — pin to a specific version
- `go mod download` — download all dependencies to cache
- `go mod vendor` — copy deps into vendor/ directory

## Resolving Dependency Issues

### "cannot find package"
1. Check the import path is correct
2. Run `go get <import-path>`
3. If it's a new module, run `go mod tidy`

### "ambiguous import"
The same package is provided by multiple modules. Pin the specific one:
```
go get specific/module@version
```

### "module requires Go >= X"
Update your go.mod's `go` directive or downgrade the dependency.

## go.sum

- `go.sum` contains cryptographic checksums for all dependencies
- Always commit `go.sum` to version control
- If `go.sum` is wrong: `go mod tidy` will regenerate it
- Never edit `go.sum` manually

## Replace Directives

For local development against a forked/local module:
```
replace github.com/some/module => ../local/path
```
Remove before committing.
