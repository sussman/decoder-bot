/*  
  Go program to decode an audio stream of Morse code into a stream of ASCII.

  See the ALGORITHM file for a description of what's going on, and
  'proof.hs' as the original proof-of-concept implementation of this
  algorithm in Haskell.
*/

package main

import (
	// "code.google.com/p/portaudio-go/portaudio"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"sort"
)


type token int

const (
	dit = iota
	dah = iota
	endLetter = iota
	endWord = iota
	pause = iota
	noOp = iota
	cwError = iota
)


// ------- Stage 1:  Detect tones in the stream. ------------------

// Use Root Mean Square (RMS) method to return 'average' value of an
// array of audio samples.
func rms(audiovals []int) int {
	sum := 0
	squaresum := 0
	for i := 0; i < len(audiovals); i++ {
		v := audiovals[i]
		sum = sum + v
		squaresum = squaresum + (v*v)
	}
	mean := sum / len(audiovals)
	meanOfSquares := squaresum / len(audiovals)
	return int(math.Sqrt(float64(meanOfSquares - (mean * mean))))
}


// Read audiosample chunks from 'chunks' channel, and push simple RMS
// amplitudes into the 'amplitudes' channel.
func amplituder(chunks chan []int, amplitudes chan int) {
	for chunk := range chunks {
		amplitudes <- rms(chunk)
	}
	close(amplitudes)
}


// Read amplitudes from 'amplitudes' channel, and push quantized
// on/off values to 'quants' channel.
func quantizer(amplitudes chan int, quants chan bool) {
	var group [100]int
	seen := 0
	max := 0
	min := 0
	for amp := range amplitudes {
		// Suck 100 amplitudes at a time from input channel,
		// figure out 'middle' amplitude for the group, and
		// use that value to quantize each amplitude.
		group[seen] = amp
		seen += 1
		if amp > max { max = amp }
		if amp < min { min = amp }				
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
func getQuantizePipe(audiochunks chan []int) chan bool {
	amplitudes := make(chan int)
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

func getRlePipe(quants chan bool) chan int {
	lengths := make(chan int)
	go func() {
		currentState := false
		tally := 0

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

// Take a list of on/off duration events, sort them, return the 25th
// percentile value as the "1 unit" duration within the time window.
//
// This magical 25% number derives from the observation that 1-unit
// silences are the most common symbol in a normal Morse phrase, so
// they should compose the majority of the bottom of the sorted pile
// of durations. In theory we could simply pick the smallest, but by
// going with the 25th percentile, the hope is to avoid picking the
// ridiculously small sample that results from a quantization error.
func calculateUnitDuration(group []int) int {
	sort.Ints(group)
	// fmt.Printf("(%d) ", group)
	return group[(len(group) / 4)]
}


// Take a normalized duration value, 'clamp' it to the magic numbers
// 1, 3, 7 (which are the faundational time durations in Morse code),
// and return a sensible semantic token.
func clamp(x float32, silence bool) token {
	if (silence) {
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


func getTokenPipe(durations chan int) chan token {
	tokens := make(chan token)
	seen := 0
	go func() {
		// As a contextual window, look at sets of 20 on/off
		// duration events when calculating the unitDuration.
		//
		// TODO(sussman): make this windowsize a constant we
		// can fiddle.
		group := make([]int, 20)
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

func main () {
	// Die on Control-C
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)

	// main input pipe:
	chunks := make(chan []int)

	// construct main output pipe... whee!
	output := getTokenPipe(getRlePipe(getQuantizePipe(chunks)))

/*	portaudio.Initialize()
	defer portaudio.Terminate()

	in := make([]int32, 64)
	stream, err := portaudio.OpenDefaultStream(1, 0, 44100, len(in), in)
	chk(err)
	defer stream.Close()
	nSamples := 0

	go func() {
		chk(stream.Start())
		for {
			chk(stream.Read())

			// chk(binary.Write(f, binary.BigEndian, in))
			
			nSamples += len(in)
			select {
			case <-sig:
				return
			default:
			}
		}
		chk(stream.Stop())
	}
*/

	// Start pushing random data into the pipeline in the background
	go func() {
		for i :=0 ; i < 5000; i++ {
			chunk := make([]int, 10)
			for j := 0; j < 10; j++ { chunk[j] = rand.Int() }
			chunks <- chunk
		}
		close(chunks)
	}()


	// Print logical tokens from the pipeline's output
	for val := range output {
		out := ""
		switch val {
		case dit: out = ". "
		case dah: out = "_ "
		case endLetter: out = "  "
		case endWord: out = ": "
		case pause: out = "pause "
		case noOp: out = ""
		default: out = "ERROR "
		}
		fmt.Printf("%s", out)
	}
	close(output)
}

