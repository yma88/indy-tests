package buildtest

import (
	"fmt"
	"math/rand"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	common "github.com/commonjava/indy-tests/common"
)

const TMP_DOWNLOAD_DIR = "/tmp/download"
const TMP_UPLOAD_DIR = "/tmp/upload"

func Run(originalIndy, foloId, replacement, targetIndy, buildType string, processNum int) {
	_, validated := common.ValidateTargetIndy(originalIndy)
	if !validated {
		os.Exit(1)
	}
	targetIndyHost, validated := common.ValidateTargetIndy(targetIndy)
	if !validated {
		os.Exit(1)
	}

	newBuildName := generateRandomBuildName()

	// Prepare the indy repos for the whole testing
	buildMeta := decideMeta(buildType)
	if !prepareIndyRepos("http://"+targetIndyHost, newBuildName, *buildMeta) {
		os.Exit(1)
	}

	// result := prepareEntriesByLog(logUrl)

	// result := prepareEntriesByFolo(originalIndy, targetIndy, foloId, newBuildName)

	origIndy := originalIndy
	if !strings.HasPrefix(origIndy, "http://") {
		origIndy = "http://" + origIndy
	}
	foloTrackContent := common.GetFoloRecord(origIndy, foloId)

	prepareCacheDirectories()

	downloads := prepareDownloadEntriesByFolo(targetIndy, newBuildName, foloTrackContent)
	downloadFunc := func(artifactUrl string) bool {
		fileLoc := path.Join(TMP_DOWNLOAD_DIR, path.Base(artifactUrl))
		return common.DownloadFile(artifactUrl, fileLoc)
	}
	broken := false
	if downloads != nil && len(downloads) > 0 {
		fmt.Println("Start handling downloads artifacts.")
		fmt.Printf("==========================================\n\n")
		if processNum > 1 {
			broken = !concurrentRunForDownload(processNum, downloads, downloadFunc)
		} else {
			for _, url := range downloads {
				broken = !downloadFunc(url)
				if broken {
					break
				}
			}
		}
		fmt.Println("==========================================")
		if broken {
			fmt.Printf("Build test failed due to some downloading errors. Please see above logs to see the details.\n\n")
			os.Exit(1)
		}
		fmt.Printf("Downloads artifacts handling finished.\n\n")
	}

	uploadFunc := func(originalArtiURL, targetArtiURL string) bool {
		cacheFile := path.Join(TMP_UPLOAD_DIR, path.Base(originalArtiURL))
		downloaded := common.DownloadUploadFileForCache(originalArtiURL, cacheFile)
		if downloaded {
			return common.UploadFile(targetArtiURL, cacheFile)
		}
		return false
	}

	uploads := prepareUploadEntriesByFolo(originalIndy, targetIndy, newBuildName, foloTrackContent)

	if uploads != nil && len(uploads) > 0 {
		fmt.Println("Start handling uploads artifacts.")
		fmt.Printf("==========================================\n\n")
		if processNum > 1 {
			broken = !concurrentRunForUpload(processNum, uploads, uploadFunc)
		} else {
			for _, artiURLs := range uploads {
				broken = !uploadFunc(artiURLs[0], artiURLs[1])
				if broken {
					break
				}
			}
		}
		fmt.Println("==========================================")
		if broken {
			fmt.Printf("Build test failed due to some uploadig errors. Please see above logs to see the details.\n\n")
			os.Exit(1)
		}

		fmt.Printf("Uploads artifacts handling finished.\n\n")
	}
}

// For downloads entries, we will get the paths and inject them to the final url of target indy
// as they should be directly download from target indy.
func prepareDownloadEntriesByFolo(targetIndyURL, newBuildId string, foloRecord common.TrackedContent) []string {
	downloads := []string{}
	targetIndy := normIndyURL(targetIndyURL)
	for _, down := range foloRecord.Downloads {
		downUrl := fmt.Sprintf("%sapi/folo/track/%s/maven/group/%s%s", targetIndy, newBuildId, newBuildId, down.Path)
		downloads = append(downloads, downUrl)
	}
	return downloads
}

// For uploads entries, firstly they should be downloaded from original indy server as they may not
// exist in target indy server, so need to use original indy server to make the download url, and
// use the target indy server to make the upload url
func prepareUploadEntriesByFolo(originalIndyURL, targetIndyURL, newBuildId string, foloRecord common.TrackedContent) map[string][]string {
	originalIndy := normIndyURL(originalIndyURL)
	targetIndy := normIndyURL(targetIndyURL)
	result := make(map[string][]string)
	for _, up := range foloRecord.Uploads {
		upTuple := []string{}
		storePath := common.StoreKeyToPath(up.StoreKey)
		uploadPath := path.Join("api/content", storePath, up.Path)
		orgiUpUrl := fmt.Sprintf("%s%s", originalIndy, uploadPath)
		upTuple = append(upTuple, orgiUpUrl)
		targUpUrl := fmt.Sprintf("%sapi/folo/track/%s/maven/group/%s%s", targetIndy, newBuildId, newBuildId, up.Path)
		upTuple = append(upTuple, targUpUrl)
		result[up.Path] = upTuple
	}

	return result
}

func normIndyURL(indyURL string) string {
	indy := indyURL
	if !strings.HasPrefix(indy, "http://") {
		indy = "http://" + indy
	}
	if !strings.HasSuffix(indy, "/") {
		indy = indy + "/"
	}
	return indy
}

func prepareCacheDirectories() {
	if !common.FileOrDirExists(TMP_DOWNLOAD_DIR) {
		os.Mkdir(TMP_DOWNLOAD_DIR, os.FileMode(0755))
	}
	if !common.FileOrDirExists(TMP_DOWNLOAD_DIR) {
		fmt.Printf("Error: cannot create directory %s for file downloading.\n", TMP_DOWNLOAD_DIR)
		os.Exit(1)
	}
	if !common.FileOrDirExists(TMP_UPLOAD_DIR) {
		os.Mkdir(TMP_UPLOAD_DIR, os.FileMode(0755))
	}
	if !common.FileOrDirExists(TMP_UPLOAD_DIR) {
		fmt.Printf("Error: cannot create directory %s for caching uploading files.\n", TMP_UPLOAD_DIR)
		os.Exit(1)
	}
}

// Deprecated as folo is the preferred way now.
func decorateChecksums(downloads []string) []string {
	downSet := make(map[string]bool)
	for _, artifact := range downloads {
		downSet[artifact] = true
		if strings.HasSuffix(artifact, ".md5") || strings.HasSuffix(artifact, ".sha1") {
			continue
		}
		downSet[artifact+".md5"] = true
		downSet[artifact+".sha1"] = true
		// downSet[artifact+".sha256"] = true
	}
	finalDownloads := []string{}
	for artifact := range downSet {
		finalDownloads = append(finalDownloads, artifact)
	}
	return finalDownloads
}

func replaceHost(artifact, oldIndyHost, targetIndyHost string) string {
	// First, replace the embedded indy host to the target one
	repl := oldIndyHost
	if common.IsEmptyString(repl) {
		repl = artifact[strings.Index(artifact, "//")+2:]
		repl = repl[:strings.Index(repl, "/")]
	}
	return strings.ReplaceAll(artifact, repl, targetIndyHost)
}

func replaceBuildName(artifact, buildName string) string {
	// Second, if use a new build name we should replace the old one with it.
	final := artifact
	if !common.IsEmptyString(buildName) {
		buildPat := regexp.MustCompile(`https{0,1}:\/\/.+\/api\/folo\/track\/(build-\S+?)\/.*`)
		buildPat.FindAllStringSubmatch(final, 0)
		matches := buildPat.FindAllStringSubmatch(final, -1)
		if matches != nil {
			for i := range matches {
				get := matches[i][1]
				if strings.HasPrefix(get, "build-") {
					final = strings.ReplaceAll(final, get, buildName)
					break
				}
			}
		}
	}
	return final
}

// generate a random 5 digit  number for a build repo like "build-test-xxxxx"
func generateRandomBuildName() string {
	buildPrefix := "build-test-"
	rand.Seed(time.Now().UnixNano())
	min := 10000
	max := 99999
	return fmt.Sprintf(buildPrefix+"%v", rand.Intn(max-min)+min)
}

func concurrentRunForDownload(numWorkers int, artifacts []string, job func(artifact string) bool) bool {
	ch := make(chan string, numWorkers*5) // This buffered number of chan can be anything as long as it's larger than numWorkers
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results = []bool{}

	// This starts numWorkers number of goroutines that wait for something to do
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func() {
			for {
				a, ok := <-ch
				if !ok { // if there is nothing to do and the channel has been closed then end the goroutine
					wg.Done()
					return
				}
				mu.Lock()
				results = append(results, job(a))
				mu.Unlock()
			}
		}()
	}

	// Now the jobs can be added to the channel, which is used as a queue
	for _, artifact := range artifacts {
		ch <- artifact // add artifact to the queue
	}

	close(ch) // This tells the goroutines there's nothing else to do
	wg.Wait() // Wait for the threads to finish

	finalResult := true
	for _, result := range results {
		if finalResult = result; !finalResult {
			break
		}
	}

	return finalResult
}

func concurrentRunForUpload(numWorkers int, artifacts map[string][]string, job func(originalArtiURL, targetArtiURL string) bool) bool {
	ch := make(chan []string, numWorkers*5) // This buffered number of chan can be anything as long as it's larger than numWorkers
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results = []bool{}

	// This starts numWorkers number of goroutines that wait for something to do
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func() {
			for {
				a, ok := <-ch
				if !ok { // if there is nothing to do and the channel has been closed then end the goroutine
					wg.Done()
					return
				}
				mu.Lock()
				results = append(results, job(a[0], a[1]))
				mu.Unlock()
			}
		}()
	}

	// Now the jobs can be added to the channel, which is used as a queue
	for _, artifact := range artifacts {
		ch <- artifact // add artifact to the queue
	}

	close(ch) // This tells the goroutines there's nothing else to do
	wg.Wait() // Wait for the threads to finish

	finalResult := true
	for _, result := range results {
		if finalResult = result; !finalResult {
			break
		}
	}

	return finalResult
}
