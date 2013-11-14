/*
  Go program to decode an audio stream of Morse code into a stream of ASCII.

  See the ALGORITHM file for a description of what's going on, and
  'proof.hs' as the original proof-of-concept implementation of this
  algorithm in Haskell.

 Requirements:
   1. Build/install portaudio C library, from http://www.portaudio.com/
   2. go get code.google.com/p/portaudio-go/portaudio

 (Originally built with 'go version go1.2rc3 darwin/amd64')

*/

package main

import (
	"code.google.com/p/portaudio-go/portaudio"
	"fmt"
	"math"
	"os"
	"os/signal"
	"sort"
)

type token int32

const (
	dit       = iota
	dah       = iota
	endLetter = iota
	endWord   = iota
	pause     = iota
	noOp      = iota
	cwError   = iota
)

// ------- Stage 1:  Detect tones in the stream. ------------------

// Use Root Mean Square (RMS) method to return 'average' value of an
// array of audio samples.
func rms(audiovals []int32) int32 {
	var sum int32 = 0
	var squaresum int32 = 0
	for i := 0; i < len(audiovals); i++ {
		v := audiovals[i]
		sum = sum + v
		squaresum = squaresum + (v * v)
	}
	var mean int32 = sum / int32(len(audiovals))
	meanOfSquares := squaresum / int32(len(audiovals))
	return int32(math.Sqrt(float64(meanOfSquares - (mean * mean))))
}

// Read audiosample chunks from 'chunks' channel, and push simple RMS
// amplitudes into the 'amplitudes' channel.
func amplituder(chunks chan []int32, amplitudes chan int32) {
	for chunk := range chunks {
		amplitudes <- rms(chunk)
	}
	close(amplitudes)
}

// Read amplitudes from 'amplitudes' channel, and push quantized
// on/off values to 'quants' channel.
func quantizer(amplitudes chan int32, quants chan bool) {
	var group [100]int32
	var seen int32 = 0
	var max int32 = 0
	var min int32 = 0
	for amp := range amplitudes {
		// Suck 100 amplitudes at a time from input channel,
		// figure out 'middle' amplitude for the group, and
		// use that value to quantize each amplitude.
		group[seen] = amp
		seen += 1
		if amp > max {
			max = amp
		}
		if amp < min {
			min = amp
		}
		if seen == 100 {
			middle := (max - min) / 2
			for i := 0; i < 100; i++ {
				quants <- (group[i] >= middle)
			}
			max = 0
			min = 0
			seen = 0
		}
	}
	close(quants)
}

// Main stage 1 pipeline: reads audiochunks from input channel;
// returns a boolean channel to which it pushes quantized on/off
// values.
func getQuantizePipe(audiochunks chan []int32) chan bool {
	amplitudes := make(chan int32)
	quants := make(chan bool)
	go amplituder(audiochunks, amplitudes)
	go quantizer(amplitudes, quants)
	return quants
}

// ------- Stage 2:  Run-length encode the on/off states. ----------
//
// That is, if the input stream is 0001100111100, we want to output
// the list [3, 2, 2, 4, 2], which can be seen as the "rhythm" of the
// coded message.

func getRlePipe(quants chan bool) chan int32 {
	lengths := make(chan int32)
	go func() {
		currentState := false
		var tally int32 = 0

		// TODO(sussman): need to "debounce" this stream
		for quant := range quants {
			if quant == currentState {
				tally += 1
			} else {
				lengths <- tally
				currentState = quant
				tally = 1
			}
		}
		close(lengths)
	}()
	return lengths
}

// ------- Stage 3: Figure out length of morse 'unit' & output logic tokens
//

type byInt32 []int32

func (b byInt32) Len() int           { return len(b) }
func (b byInt32) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }
func (b byInt32) Less(i, j int) bool { return b[i] < b[j] }

// Take a list of on/off duration events, sort them, return the 25th
// percentile value as the "1 unit" duration within the time window.
//
// This magical 25% number derives from the observation that 1-unit
// silences are the most common symbol in a normal Morse phrase, so
// they should compose the majority of the bottom of the sorted pile
// of durations. In theory we could simply pick the smallest, but by
// going with the 25th percentile, the hope is to avoid picking the
// ridiculously small sample that results from a quantization error.
func calculateUnitDuration(group []int32) int32 {
	sort.Sort(byInt32(group))
	return group[int32((len(group) / 4))]
}

// Take a normalized duration value, 'clamp' it to the magic numbers
// 1, 3, 7 (which are the faundational time durations in Morse code),
// and return a sensible semantic token.
func clamp(x float32, silence bool) token {
	if silence {
		switch {
		case x > 8:
			return pause
		case x > 5:
			return endWord
		case x > 2:
			return endLetter
		default:
			return noOp
		}
	} else {
		switch {
		case x > 8:
			return cwError
		case x > 5:
			return cwError
		case x > 2:
			return dah
		default:
			return dit
		}
	}
	return cwError
}

func getTokenPipe(durations chan int32) chan token {
	tokens := make(chan token)
	seen := 0
	go func() {
		// As a contextual window, look at sets of 20 on/off
		// duration events when calculating the unitDuration.
		//
		// TODO(sussman): make this windowsize a constant we
		// can fiddle.
		group := make([]int32, 20)
		for duration := range durations {
			group[seen] = duration
			seen += 1
			if seen == 20 {
				seen = 0

				// figure out the length of a 'dit' (1 unit)
				unitDuration := calculateUnitDuration(group[:])

				// normalize & clamp each duration by this
				silence := false
				for i := range group {
					norm := float32(group[i] / unitDuration)
					tokens <- clamp(norm, silence)
					silence = !silence
				}
			}
		}
		close(durations)
	}()
	return tokens
}

// ------ Put all the pipes together. --------------

func chk(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	// Die on Control-C
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)

	// main input pipe:
	chunks := make(chan []int32)

	// construct main output pipe... whee!
	output := getTokenPipe(getRlePipe(getQuantizePipe(chunks)))

	// read samples from microphone, via portaudio library
	portaudio.Initialize()
	defer portaudio.Terminate()
	samplechunk := make([]int32, 64)
	stream, err := portaudio.OpenDefaultStream(1, 0, 44100, len(samplechunk), samplechunk)
	chk(err)
	defer stream.Close()
	nSamples := 0

	go func() {
		chk(stream.Start())
		for {
			chk(stream.Read())

			// chk(binary.Write(f, binary.BigEndian, in))
			chunks <- samplechunk

			nSamples += len(samplechunk)
			select {
			case <-sig:
				return
			default:
			}
		}
		chk(stream.Stop())
	}()

	// Print logical tokens from the pipeline's output
	for val := range output {
		out := ""
		switch val {
		case dit:
			out = "."
		case dah:
			out = "_"
		case endLetter:
			out = " "
		case endWord:
			out = " : "
		case pause:
			out = " pause "
		case noOp:
			out = ""
		default:
			out = " ERROR "
		}
		fmt.Printf("%s", out)
	}
	close(output)
}
