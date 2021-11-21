package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog/log"
	_ "go.uber.org/automaxprocs"
	tb "gopkg.in/tucnak/telebot.v3"
)

const (
	concurrentYoutubeDlWorkers = 3
)

var (
	youtubeDlSemaphore chan struct{}
	inMemCache         *cache.Cache
	inMemCacheEnabled  bool
)

func main() {
	if os.Getenv("NO_CACHE") != "true" {
		inMemCacheEnabled = true
		inMemCache = initInMemCache()
	}

	// init Telegram bot client
	b, err := tb.NewBot(tb.Settings{
		Token:  os.Getenv("BOT_TOKEN"),
		Poller: &tb.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to telegram bot api")
		return
	}

	// set semaphore capacity to limit number of concurrent youtube-dl processes
	var concurrentWorkers int64 = concurrentYoutubeDlWorkers
	if conWorkersStr := os.Getenv("CONCURRENT_WORKERS"); conWorkersStr != "" {
		conWorkers, err := strconv.ParseInt(conWorkersStr, 10, 64)
		if err != nil {
			log.Fatal().Err(err).Msg("CONCURRENT_WORKERS must be valid integer")
		} else if conWorkers < 1 || conWorkers > 10 {
			log.Fatal().Msg("CONCURRENT_WORKERS must be in range between 1 and 10")
		}

		concurrentWorkers = conWorkers
	}
	youtubeDlSemaphore = make(chan struct{}, concurrentWorkers)

	// add handler for /start command
	b.Handle("/start", func(c tb.Context) error {
		return c.Send("Send me youtube video link. And I'll send you download links.")
	})

	// add handler for /service_list command
	// shows the list of all supported streaming services
	b.Handle("/service_list", func(c tb.Context) error {
		return c.Send("Send me youtube video link. And I'll send you download links.")
	})

	// add handler for any text message
	b.Handle(tb.OnText, func(c tb.Context) error {
		log.Info().Str("message", c.Text()).Msg("received message")

		var (
			result *VideoInfo
			errG   error
		)

		// get video info from cache
		result = getVideoInfoFromCache(c.Text())
		if result == nil {
			// send temporary message to show the work has started
			tmpMsg, err := c.Bot().Send(c.Sender(), "gathering info...")
			if err != nil {
				log.Error().Err(err).Msg("failed to send temporary message")
			}
			// delete this temporary message after all
			defer b.Delete(tmpMsg)

			// get video info and download links via youtube-dl
			err = retry.Do(
				func() error {
					result, errG = getVideoData(context.Background(), c.Text())
					if errG != nil {
						log.Debug().Err(err).Msg("parse video url attempt failed")
						return errG
					}

					// save video info to the cache
					saveVideoInfoToCache(c.Text(), result)

					return nil
				},
				retry.Attempts(10),
			)
			if err != nil {
				log.Error().Err(err).Msg("failed to parse video url")
				_ = c.Reply("failed to parse video url")
				return err
			}
		}

		links := make([]*Link, 0)
		for _, f := range result.Formats {
			link := new(Link)

			format, url, ext, asr, height, bytesSize := f.Format, f.Url, f.Ext, f.Asr, f.Height, f.Filesize

			t := strings.Split(format, " - ")

			size := ""
			if bytesSize > 0 {
				size = fmt.Sprintf(" (%s)", bytesToHumanReadableSize(int64(bytesSize)))
			}

			var item string
			if height == 0 {
				link.Type = LinkTypeAudioOnly
				link.DownloadUrl = fmt.Sprintf("<a href='%s'>only audio %.0f (%s)%s</a>", url, asr, ext, size)
			} else {
				item = strings.Join(t[1:], "")
				link.DownloadUrl = fmt.Sprintf("<a href='%s'>video with audio %s (%s)%s</a>", url, item, ext, size)
				link.Type = LinkTypeVideoOnly
				if asr > 0 {
					link.Type = LinkTypeVideoWithAudio
				}
			}

			links = append(links, link)
		}

		sort.Slice(links, func(i, j int) bool {
			return links[i].Type > links[j].Type
		})

		// send all messages
		index := 1
		msg := ""
		for _, link := range links {
			// drop video only links
			if link.Type == LinkTypeVideoOnly {
				continue
			}

			item := fmt.Sprintf("%d. %s\n", index, link.DownloadUrl)

			// Telegram message body is limited by 4096 bytes, so we add links to
			// the message until its size exceed the limit, then send message and
			// start filling in the new one
			if len(msg)+len(link.DownloadUrl) < 4096 {
				msg += item
			} else {
				if err := c.Reply(msg, tb.ModeHTML, tb.NoPreview); err != nil {
					log.Error().Err(err).Str("message_body", msg).Msg("failed to send message")
				}
				msg = item
			}
			index++
		}

		// if the message body is not empty then send it
		if len(msg) > 0 {
			if err := c.Reply(msg, tb.ModeHTML, tb.NoPreview); err != nil {
				log.Error().Err(err).Str("message_body", msg).Msg("failed to send message")
			}
		}

		return nil
	})

	log.Info().Msg("bot listens to new messages")

	b.Start()
}

func getVideoData(ctx context.Context, url string) (*VideoInfo, error) {
	youtubeDlSemaphore <- struct{}{}
	defer func() {
		<-youtubeDlSemaphore
	}()

	cmd := exec.CommandContext(
		ctx,
		"youtube-dl",
		"--ignore-errors",
		"--no-call-home",
		"--no-cache-dir",
		"--skip-download",
		"--youtube-skip-dash-manifest",
		// provide URL via stdin for security, youtube-dl has some run command args
		"--batch-file", "-",
		"-J",
	)

	tempPath, _ := ioutil.TempDir("", "ydls")
	defer os.RemoveAll(tempPath)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	stderrWriter := ioutil.Discard

	cmd.Dir = tempPath
	cmd.Stdout = stdoutBuf
	cmd.Stdin = bytes.NewBufferString(url + "\n")
	cmd.Stderr = io.MultiWriter(stderrBuf, stderrWriter)

	cmdErr := cmd.Run()
	if cmdErr != nil {
		return nil, cmdErr
	}

	stderrLineScanner := bufio.NewScanner(stderrBuf)
	for stderrLineScanner.Scan() {
		const errorPrefix = "ERROR: "
		line := stderrLineScanner.Text()
		if strings.HasPrefix(line, errorPrefix) {
			return nil, fmt.Errorf("failed to get video: %s", line[len(errorPrefix):])
		}
	}

	var info VideoInfo
	if len(stdoutBuf.Bytes()) > 0 {
		if infoErr := json.Unmarshal(stdoutBuf.Bytes(), &info); infoErr != nil {
			return nil, infoErr
		}
	} else {
		return nil, fmt.Errorf("failed to get video info")
	}

	return &info, nil
}

type VideoInfo struct {
	ID      string        `json:"id"`    // Video identifier
	Title   string        `json:"title"` // Video title
	Formats []VideoFormat `json:"formats"`
}

type VideoFormat struct {
	Ext      string  `json:"ext"`      // Video filename extension
	Format   string  `json:"format"`   // A human-readable description of the format
	Width    float64 `json:"width"`    // Width of the video
	Height   float64 `json:"height"`   // Height of the video
	Asr      float64 `json:"asr"`      // Audio sampling rate in Hertz
	Filesize float64 `json:"filesize"` // The number of bytes, if known in advance
	Url      string  `json:"url"`      // Url to download file
}

type Link struct {
	OriginUrl   string
	DownloadUrl string
	Type        LintType
}

type LintType int

const (
	LinkTypeAudioOnly LintType = iota
	LinkTypeVideoOnly
	LinkTypeVideoWithAudio
)

func bytesToHumanReadableSize(b int64) string {
	const unit = 1 << 10

	if b < unit {
		return fmt.Sprintf("%dB", b)
	}

	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "kMGTPE"[exp])
}

func initInMemCache() *cache.Cache {
	return cache.New(30*time.Minute, 10*time.Minute)
}

func saveVideoInfoToCache(videoUrl string, video *VideoInfo) {
	if !inMemCacheEnabled {
		return
	}

	h := sha1.New()
	h.Write([]byte(videoUrl))
	bs := h.Sum(nil)

	inMemCache.Set(string(bs), video, cache.DefaultExpiration)
}

func getVideoInfoFromCache(videoUrl string) *VideoInfo {
	if !inMemCacheEnabled {
		return nil
	}

	h := sha1.New()
	h.Write([]byte(videoUrl))
	bs := h.Sum(nil)

	l, ok := inMemCache.Get(string(bs))
	if !ok {
		return nil
	}

	return l.(*VideoInfo)
}
