package main

import (
	"bufio"
	"bytes"
	"context"
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
	"github.com/rs/zerolog/log"
	_ "go.uber.org/automaxprocs"
	tb "gopkg.in/tucnak/telebot.v2"
)

const (
	concurrentYoutubeDlWorkers = 3
)

var (
	youtubeDlSemaphore chan struct{}
)

func main() {
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
	b.Handle("/start", func(m *tb.Message) {
		b.Send(m.Sender, "Send me youtube video link. And I'll send you download links.")
	})

	// add handler for any text message
	b.Handle(tb.OnText, func(m *tb.Message) {
		log.Info().Str("message", m.Text).Msg("received message")

		// send temporary message to show the work has started
		mm, err := b.Send(m.Sender, "gathering info...")
		if err != nil {
			log.Error().Err(err).Msg("failed to send temporary message")
		}
		// delete this temporary message after all
		defer b.Delete(mm)

		var (
			result *VideoInfo
			errG   error
		)

		// get video info and download links via youtube-dl
		err = retry.Do(
			func() error {
				result, errG = getVideoData(context.Background(), m.Text)
				if errG != nil {
					log.Debug().Err(err).Msg("parse video url attempt failed")
					return errG
				}

				return nil
			},
			retry.Attempts(10),
		)
		if err != nil {
			log.Error().Err(err).Msg("failed to parse video url")
			_, _ = b.Reply(m, "failed to parse video url")
			return
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
				link.Link = fmt.Sprintf("<a href='%s'>only audio %.0f (%s)%s</a>", url, asr, ext, size)
			} else {
				item = strings.Join(t[1:], "")
				link.Link = fmt.Sprintf("<a href='%s'>video with audio %s (%s)%s</a>", url, item, ext, size)
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

			item := fmt.Sprintf("%d. %s\n", index, link.Link)

			// Telegram message body is limited by 4096 bytes, so we add links to
			// the message until its size exceed the limit, then send message and
			// start filling in the new one
			if len(msg)+len(link.Link) < 4096 {
				msg += item
			} else {
				if _, err := b.Send(m.Sender, msg, tb.ModeHTML, tb.NoPreview); err != nil {
					log.Error().Err(err).Str("message_body", msg).Msg("failed to send message")
				}
				msg = item
			}
			index++
		}

		// if the message body is not empty then send it
		if len(msg) > 0 {
			if _, err := b.Send(m.Sender, msg, tb.ModeHTML, tb.NoPreview); err != nil {
				log.Error().Err(err).Str("message_body", msg).Msg("failed to send message")
			}
		}
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
	Link string
	Type LintType
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
