package main

import (
	"bytes"
	"io"
	"log"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/karrick/godirwalk"
	"github.com/mitchellh/go-homedir"
)

func dealUploadFilePath(uploadFilepath string, postFileCh chan string) {
	if uploadFilepath == "" {
		return
	}

	uploadFilepath, _ = homedir.Expand(uploadFilepath)
	fs, err := os.Stat(uploadFilepath)
	if err != nil && os.IsNotExist(err) {
		log.Fatalf("%s dos not exist", uploadFilepath)
	}
	if err != nil {
		log.Fatalf("stat file %s error  %v", uploadFilepath, err)
	}

	defer close(postFileCh)

	if !fs.IsDir() {
		postFileCh <- uploadFilepath
		return
	}

	fn := func(osPathname string, directoryEntry *godirwalk.Dirent) error {
		if v, e := directoryEntry.IsDirOrSymlinkToDir(); v || e != nil {
			return e
		}

		if strings.HasPrefix(directoryEntry.Name(), ".") {
			return nil
		}

		postFileCh <- osPathname
		return nil
	}
	options := godirwalk.Options{Unsorted: true, Callback: fn}
	if err := godirwalk.Walk(uploadFilepath, &options); err != nil {
		log.Printf("walk dir: %s error: %v", uploadFilepath, err)
	}
}

var filePathCache sync.Map

type cacheItem struct {
	data        []byte
	contentType string
}

// readMultipartFile read file filePath for upload in multipart,
// return multipart content, form data content type and error.
func readMultipartFile(fieldName, filePath string) (data []byte, contentType string, err error) {
	if load, ok := filePathCache.Load(filePath); ok {
		item := load.(cacheItem)
		return item.data, item.contentType, nil
	}

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)

	part, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
	if err != nil {
		return nil, "", err
	}

	file := OpenFile(filePath)
	defer file.Close()

	_, _ = io.Copy(part, file)
	_ = writer.Close()

	item := cacheItem{data: buffer.Bytes(), contentType: writer.FormDataContentType()}
	filePathCache.Store(filePath, item)

	return item.data, item.contentType, nil
}

// OpenFile opens file successfully or panic.
func OpenFile(f string) *os.File {
	r, err := os.Open(f)
	if err != nil {
		panic(err)
	}

	return r
}
