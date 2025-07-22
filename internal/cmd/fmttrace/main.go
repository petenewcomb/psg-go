// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/exp/trace"
)

func main() {
	if len(os.Args) < 1 || len(os.Args) > 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s [<trace-file>]\n", os.Args[0])
		os.Exit(1)
	}

	var input io.Reader
	if len(os.Args) == 1 || os.Args[1] == "-" {
		input = os.Stdin
	} else {
		filename := os.Args[1]
		file, err := os.Open(filename)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening file: %v\n", err)
			os.Exit(1)
		}
		defer file.Close()
		input = file
	}

	reader, err := trace.NewReader(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating trace reader: %v\n", err)
		os.Exit(1)
	}

	var startTimeNs, prevTimeNs trace.Time
	var prevThreadID trace.ThreadID
	var prevProcID trace.ProcID
	var prevGoroutineID trace.GoID
	regionStackMap := make(map[trace.GoID][]string)
	prevEventTimeMap := make(map[trace.GoID]trace.Time)
	firstEvent := true
	firstHeader := true

	flushHeader := func(event trace.Event) {
		timeSinceStart := time.Duration(event.Time()-startTimeNs) * time.Nanosecond
		timeSincePrevEvent := time.Duration(event.Time()-prevTimeNs) * time.Nanosecond
		if timeSincePrevEvent < 0 {
			panic("expecting events to be in chronological order")
		}
		prevTimeNs = event.Time()

		threadID := event.Thread()
		procID := event.Proc()
		goroutineID := event.Goroutine()

		goroutinePrevEventTimeNs, ok := prevEventTimeMap[goroutineID]
		var goroutineTimeSincePrevEvent time.Duration
		if ok {
			goroutineTimeSincePrevEvent = time.Duration(event.Time()-goroutinePrevEventTimeNs) * time.Nanosecond
		}
		prevEventTimeMap[goroutineID] = event.Time()

		if firstEvent || threadID != prevThreadID || procID != prevProcID || goroutineID != prevGoroutineID {
			var threadIDString string
			if threadID == trace.NoThread {
				threadIDString = "<NONE>"
			} else {
				threadIDString = fmt.Sprint(threadID)
			}

			var procIDString string
			if procID == trace.NoProc {
				procIDString = "<NONE>"
			} else {
				procIDString = fmt.Sprint(procID)
			}

			var goroutineIDString string
			if goroutineID == trace.NoGoroutine {
				goroutineIDString = "<NONE>"
			} else {
				goroutineIDString = fmt.Sprint(goroutineID)
			}

			if firstHeader {
				firstHeader = false
			} else {
				fmt.Println()
			}

			fmt.Printf("%012d (+%10v) G=%s P=%s M=%s\n",
				timeSinceStart.Nanoseconds(), timeSincePrevEvent, goroutineIDString, procIDString, threadIDString)

			prevThreadID = threadID
			prevProcID = procID
			prevGoroutineID = goroutineID
		}

		indent := strings.Repeat("  ", len(regionStackMap[goroutineID]))

		fmt.Printf("%012d (+%10v)   %s", timeSinceStart.Nanoseconds(), goroutineTimeSincePrevEvent, indent)
	}

	for {
		event, err := reader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "Error reading event: %v\n", err)
			os.Exit(1)
		}

		if firstEvent {
			startTimeNs = event.Time()
			prevTimeNs = startTimeNs
			firstEvent = false
		}

		switch event.Kind() {
		case trace.EventLog:
			flushHeader(event)
			log := event.Log()
			regionStack := regionStackMap[event.Goroutine()]
			category := log.Category
			if len(regionStack) > 0 {
				regionType := regionStack[len(regionStack)-1]
				if strings.HasPrefix(category, regionType) {
					if len(category) == len(regionType) {
						category = ""
					} else if len(category) > len(regionType) && category[len(regionType)] == '.' {
						category = category[len(regionType)+1:]
					}
				}
			}
			if len(category) > 0 {
				fmt.Printf("%s: %s\n", category, log.Message)
			} else {
				fmt.Printf("%s\n", log.Message)
			}

		case trace.EventRegionBegin:
			flushHeader(event)
			region := event.Region()
			fmt.Printf("%s: begin\n", region.Type)
			regionStackMap[event.Goroutine()] = append(regionStackMap[event.Goroutine()], region.Type)

		case trace.EventRegionEnd:
			region := event.Region()
			regionStack := regionStackMap[event.Goroutine()]
			if regionStack[len(regionStack)-1] != event.Region().Type {
				panic("expected region end type to match region begin type")
			}
			regionStackMap[event.Goroutine()] = regionStack[:len(regionStack)-1]
			flushHeader(event)
			fmt.Printf("%s: end\n", region.Type)
		}
	}
}
