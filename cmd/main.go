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
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/tucnak/telebot.v2"
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

		// get video info and download links via youtube-dl
		result, err := getVideoData(context.Background(), m.Text)
		if err != nil {
			log.Error().Err(err).Msg("failed to parse video url")
			b.Reply(m, "failed to parse video url")
			return
		}

		// Telegram message body length is limited by 4096 bytes, su we will divide
		// result list of links into a few separate messages

		var (
			// messageLinks is the list of links in each message
			// if length of all links in a message + new link length exceeds 4096 bytes
			// then create a new message and add new link into this message
			messageLinks         = make([][]string, 1)
			curMsgNum, curMsgLen int
		)
		for i, f := range result.Formats {
			format, url, ext, asr, height, bytesSize := f.Format, f.Url, f.Ext, f.Asr, f.Height, f.Filesize

			t := strings.Split(format, " - ")

			size := ""
			if bytesSize > 0 {
				size = fmt.Sprintf(" (%s)", bytesToHumanReadableSize(int64(bytesSize)))
			}

			var item string
			if height == 0 {
				item = fmt.Sprintf("%d. <a href='%s'>audio %.0f (%s)%s</a>", i+1, url, asr, ext, size)
			} else {
				// ignore video without sound
				if asr == 0 {
					continue
				}

				item = strings.Join(t[1:], "")
				item = fmt.Sprintf("%d. <a href='%s'>video %s (%s)%s</a>", i+1, url, item, ext, size)
			}

			if curMsgLen+len(item) > 4096 {
				curMsgNum++
				curMsgLen = 0
				messageLinks = append(messageLinks, make([]string, 0))
			}

			messageLinks[curMsgNum] = append(messageLinks[curMsgNum], item)
			curMsgLen += len(item)
		}

		// send all messages
		for i := range messageLinks {
			msg := strings.Join(messageLinks[i], "\n")
			if _, err := b.Send(m.Sender, msg, tb.ModeHTML, tb.NoPreview); err != nil {
				log.Error().Err(err).Str("message_body", msg).Msg("failed to send message")
			}
		}
	})

	log.Info().Msg("bot listens to new messages")

	b.Start()
}

func getVideoData(ctx context.Context, url string) (*VideoInfo, error) {
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
