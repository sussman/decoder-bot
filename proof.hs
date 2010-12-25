-- cabal install WAVE
-- runghc proof.hs
--
-- The input file has to be called sound.wav in the current dir.
--
-- Haskell proof-of-concept of stages 1/2/3 (see ALGORITHM).

module Main where

import Control.Monad
import Data.WAVE
import Data.Int
import Data.List (group, sort)

-- Take a flat stream and break it into chunks of at most N elements.
chunkBy :: Int -> [a] -> [[a]]
chunkBy _ [] = []
chunkBy n xs = let (as,bs) = splitAt n xs in as : chunkBy n bs

-- The first analysis step analyzes small chunks of raw sound samples
-- (tens of milliseconds at a time) and quantizes each chunk to either
-- "tone" or "no tone".
analysisStep1 :: [Integer] -> Int -> [Bool]
analysisStep1 samples sampleRate = let
    --
    -- 1.1: compute the number of samples per chunk for the
    -- desired quantization window.
    quantizationWindowMs :: Int
    quantizationWindowMs = 5
    samplesInWindow :: Int
    samplesInWindow = 
        (fromIntegral quantizationWindowMs) * sampleRate `quot` 1000

    -- 1.2: chunk up the stream into ~10ms chunks.
    sampleChunks :: [[Integer]]
    sampleChunks = chunkBy samplesInWindow samples

    -- 1.3: for each chunk, compute the maximum amplitude of the
    -- sound wave in that chunk. This collapses each chunk back
    -- into a single value, which is the very crude approximation
    -- of the amount of sound energy in that ~10ms window.
    amplitudes :: [Integer]
    amplitudes = map (\x -> maximum x - minimum x) sampleChunks
        
    -- 1.4: bundle up these amplitudes into groups such that each
    -- group represents ~1s of sound.
    quantizationAnalysisWindow :: Int
    quantizationAnalysisWindow = 1000 `quot` quantizationWindowMs
    amplitudeChunks :: [[Integer]]
    amplitudeChunks = chunkBy quantizationAnalysisWindow amplitudes
        
    -- 1.5: within each chunk of amplitudes, compute the middle
    -- amplitude, and use that middle to quantize all amplitudes
    -- to either 0 or 1.
    quantize :: [Integer] -> [Bool]
    quantize amplitudes = map (> discriminator) amplitudes
        where discriminator :: Integer
              discriminator = smallest + middle
              middle :: Integer
              middle = (biggest - smallest) `quot` 2
              biggest = maximum amplitudes
              smallest = minimum amplitudes

    -- step 1 complete: quantizedStream tells us whether each of
    -- the 10ms blocks of the input sound contains a tone or
    -- silence.
    quantizedStream :: [Bool]
    quantizedStream = concatMap quantize amplitudeChunks
    in quantizedStream

-- The second analysis step converts a stream of quantized 10ms states
-- into a stream of state lengths. That is, if the input stream is
-- 0001100111100 (with 1 = tone and 0 = silence), we want to output
-- the list [3, 2, 2, 4, 2], which can be seen as the "rhythm" of the
-- coded message.
--
-- The operation to do this is a trivial run-length encoding of the
-- input stream. And delightfully, run-length encoding is trivial to
-- accomplish in Haskell.
type Duration = Int
analysisStep2 :: [Bool] -> [Duration]
analysisStep2 = map length . group

-- The third analysis step determines the unit length of the coded
-- phrase (the length of one inter-symbol silence), and from there
-- translates each duration into its standardized value (in Morse, all
-- symbols, both tones and silences, are 1, 3 and 7 units long)
analysisStep3 :: [Duration] -> [Duration]
analysisStep3 durations = let
    -- Again, we need to consider elements within their context. Here,
    -- we chunk up into groups of 20 symbols (10 tones and 10
    -- silences).
    analysisGroup :: Int
    analysisGroup = 20
    durationChunks :: [[Duration]]
    durationChunks = chunkBy analysisGroup durations

    -- Within each chunk, we sort the durations, and select the 25th
    -- percentile duration as our 1-unit duration. This magical 25%
    -- number derives from the observation that 1-unit silences are
    -- the most common symbol in a normal Morse phrase, so they should
    -- compose the majority of the bottom of the sorted pile of
    -- durations. In theory we could simply pick the smallest, but by
    -- going with the 25th percentile, the hope is to avoid picking
    -- the extreme, ridiculously small sample that results from a
    -- quantization error.
    unit :: [Duration] -> Duration
    unit durations = (sort durations) !! (length durations `quot` 4)
    chunkUnits = map unit durationChunks
    
    -- Now that we have a unit duration for each duration chunk, use
    -- it to normalize each duration chunk, and output them all as a
    -- single stream again. Note that we do the normalization in
    -- floating point, so that the normalization is more precise than
    -- brutal truncating integer division.
    normalize :: Duration -> [Duration] -> [Double]
    normalize unit durations =
        map (\x -> fromIntegral x / fromIntegral unit) durations
    normalizedApproximateStream :: [Double]
    normalizedApproximateStream =
        concatMap (uncurry normalize) (zip chunkUnits durationChunks)
    
    -- Finally, we try to clamp each value in the stream to one of 1,
    -- 3 or 7, by rounding each double to the nearest of these
    -- values. We do know however that there can be silences much
    -- longer than 7 units as operators pause between sentences, so
    -- anything greater than 8 units long gets rounded up to 10, which
    -- we'll consider our "break" value.
    clamp :: Double -> Duration
    clamp x | x > 8     = 10
            | x > 5     = 7
            | x > 2     = 3
            | otherwise = 1
    in map clamp normalizedApproximateStream

data Token = Dit | Dah | EndLetter | EndWord | Pause | Error deriving (Show)
analysisStep4 :: [Duration] -> [Token]
analysisStep4 ds = tone (tail ds)
  where
    tone [] = []
    tone (1:ds) = Dit : silence ds
    tone (3:ds) = Dah : silence ds
    tone (_:ds) = Error : tone ds
    silence [] = []
    silence (1:ds) = tone ds
    silence (3:ds) = EndLetter : tone ds
    silence (7:ds) = EndWord : tone ds
    silence (10:ds) = Pause : tone ds
    silence (_:ds) = Error : tone ds

analyze :: [Integer] -> Int -> [Token]
analyze samples sampleRate = let
    toneStream :: [Bool]
    toneStream = analysisStep1 samples sampleRate

    durationStream :: [Duration]
    durationStream = analysisStep2 toneStream

    normalizedDurationStream :: [Duration]
    normalizedDurationStream = analysisStep3 durationStream

    tokenStream :: [Token]
    tokenStream = analysisStep4 normalizedDurationStream
    in tokenStream

nice :: Token -> String
nice Dit = "."
nice Dah = "-"
nice EndLetter = " "
nice EndWord = "\n"
nice Pause = "\n\n"

main = do
    wav <- getWAVEFile "sound.wav"
    -- Before analysis, we convert the sound samples from Int32 to
    -- Integer. This allows us to ignore integer overflow issues, at
    -- the cost of performance.
    let
        samples :: [Integer]
        samples = map (fromIntegral . head) (waveSamples wav)
    -- Fire off the analysis and print the result.
        
        result = analyze samples (fromIntegral $ waveFrameRate $ waveHeader wav)
    mapM_ (putStr . nice) result
    putStrLn ""
