package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"zee/audio"
	"zee/beep"
	"zee/clipboard"
	"zee/encoder"
	"zee/hotkey"
	"zee/log"
)

func runTestMode(wavPath string) {
	beep.Disable()

	if err := log.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not init logging: %v\n", err)
	}
	defer log.Close()

	log.SessionStart(activeTranscriber.Name(), activeFormat, activeFormat)

	if autoPaste {
		if err := clipboard.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: paste init failed: %v\n", err)
		}
	}

	fakeCtx, err := audio.NewFakeContext(wavPath, streamEnabled)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading WAV: %v\n", err)
		os.Exit(1)
	}

	capture, err := fakeCtx.NewCapture(nil, audio.CaptureConfig{
		SampleRate: encoder.SampleRate, Channels: encoder.Channels,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating capture: %v\n", err)
		os.Exit(1)
	}
	defer capture.Close()

	fakeCapture := capture.(*audio.FakeCapture)
	hk := hotkey.NewFake()
	recordingDone := make(chan struct{}, 1)

	// Stdin driver in background -- sends hotkey events, handles WAIT/SLEEP/QUIT
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			cmd := strings.TrimSpace(scanner.Text())
			switch cmd {
			case "KEYDOWN":
				hk.SimKeydown()
			case "KEYUP":
				hk.SimKeyup()
			case "WAIT":
				<-recordingDone
			case "WAIT_AUDIO_DONE":
				<-fakeCapture.AudioDone()
			case "QUIT":
				log.SessionEnd(len(transcriptions))
				os.Exit(0)
			default:
				if strings.HasPrefix(cmd, "SLEEP ") {
					if ms, err := strconv.Atoi(cmd[6:]); err == nil {
						time.Sleep(time.Duration(ms) * time.Millisecond)
					}
				}
			}
		}
		os.Exit(0)
	}()

	// Event loop -- same pattern as run()
	for {
		<-hk.Keydown()
		done, err := handleRecording(capture, hk.Keyup())
		if err != nil {
			log.Errorf("recording error: %v", err)
		}
		if done != nil {
			<-done
		}
		select {
		case recordingDone <- struct{}{}:
		default:
		}
	}
}
