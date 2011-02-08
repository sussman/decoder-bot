/*  Go program to decode an audio stream of morse code into a stream of ascii.

*/

package main

import (
	"fmt"
	"math"
	"rand"
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
	mean_of_squares := squaresum / len(audiovals)
	return int(math.Sqrt(float64(mean_of_squares - (mean * mean))))
}


// Reads audiosample chunks from 'chunks' channel, pushes simple RMS
// amplitudes into the 'amplitudes' channel.
func amplituder(chunks chan []int, amplitudes chan int) {
	for {
		chunk := <-chunks
		amplitudes <- rms(chunk)
	}
}


// Reads amplitudes from 'amplitudes' channel, and pushes quantized
// on/off values to 'quants' channel.
func quantizer(amplitudes chan int, quants chan bool) {
	for {
		// Suck 100 amplitudes at a time from input channel,
		// figure out 'middle' amplitude in the group, and use
		// this value to quantize each amplitude.
		var group [100]int
		max := 0
		min := 0
		for i := 0; i < 100; i++ {
			amp := <-amplitudes
			group[i] = amp
			if amp > max { max = amp }
			if amp < min { min = amp }				
		}
		middle := (max - min) / 2
		for i := 0; i < 100; i++ {
			quants <- (group[i] >= middle)
		}
	}
}


// Main stage 1 pipeline: reads audiochunks from input channel;
// returns a boolean channel to which it pushes quantized on/off
// values.
func get_quantize_pipe(audiochunks chan []int) chan bool {
	amplitudes := make(chan int)
	quants := make(chan bool)
	go amplituder(audiochunks, amplitudes)
	go quantizer(amplitudes, quants)
	return quants
}




// ------ Put all the pipes together. --------------

func main () {
	chunks := make(chan []int)
	quants := get_quantize_pipe(chunks)

	// Push a crapload of random data into the pipeline
	for i :=0 ; i < 5000; i++ {
		chunk := make([]int, 10)
		for j := 0; j < 10; j++ { chunk[j] = rand.Int() }
		chunks <- chunk
	}

	// Pull quantized booleans from the pipeline's output
	for {
		fmt.Printf("%d ", <-quants)
	}
}

