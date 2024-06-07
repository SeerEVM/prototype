package main

import (
	"github.com/urfave/cli/v2"
	"log"
	"os"
	"runtime"
	"seerEVM/experiments"
)

const (
	TestPreExecutionLarge = iota
	TestPrediction
	TestSpeedup
	TestConcurrent
	TestAbort
	TestBreakDown
	TestMemoryCostBaseline
	TestMemoryCost
)

type SeerRun struct {
	// flags to determine which to run
	TestIndicator    int
	StartingBlock    int64
	BlockNum         int64
	TxNum            int
	Threads          int
	DisorderRatio    float64
	EnableRepair     bool
	EnablePerceptron bool
	EnableFast       bool
	StoreCheckpoint  bool
}

func (sr *SeerRun) Run() {
	app := &cli.App{
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:        "indicator",
				Aliases:     []string{"in"},
				Usage:       "Specify the test content",
				Value:       TestPreExecutionLarge,
				Destination: &sr.TestIndicator,
			},
			&cli.Int64Flag{
				Name:        "start",
				Aliases:     []string{"st"},
				Usage:       "Starting block height",
				Value:       14650000,
				Destination: &sr.StartingBlock,
			},
			&cli.Int64Flag{
				Name:        "blockNum",
				Aliases:     []string{"bn"},
				Usage:       "Number of blocks to replay",
				Value:       100,
				Destination: &sr.BlockNum,
			},
			&cli.IntFlag{
				Name:        "txNum",
				Aliases:     []string{"tn"},
				Usage:       "Number of transactions to replay (in large scale)",
				Value:       2000,
				Destination: &sr.TxNum,
			},
			&cli.IntFlag{
				Name:        "threads",
				Aliases:     []string{"th"},
				Usage:       "Number of threads for concurrent execution",
				Value:       4,
				Destination: &sr.Threads,
			},
			&cli.Float64Flag{
				Name:        "ratio",
				Aliases:     []string{"r"},
				Usage:       "Ratio of out-of-order transactions",
				Value:       0,
				Destination: &sr.DisorderRatio,
			},
			&cli.BoolFlag{
				Name:        "repair",
				Aliases:     []string{"rp"},
				Value:       false,
				Usage:       "Whether to enable pre-execution repair",
				Destination: &sr.EnableRepair,
			},
			&cli.BoolFlag{
				Name:        "perceptron",
				Aliases:     []string{"pc"},
				Value:       false,
				Usage:       "Whether to enable perceptron model",
				Destination: &sr.EnablePerceptron,
			},
			&cli.BoolFlag{
				Name:        "fast",
				Aliases:     []string{"f"},
				Value:       false,
				Usage:       "Whether to enable fast-path execution",
				Destination: &sr.EnableFast,
			},
			&cli.BoolFlag{
				Name:        "checkpoint",
				Aliases:     []string{"cp"},
				Value:       false,
				Usage:       "Whether to enable checkpoint caching",
				Destination: &sr.StoreCheckpoint,
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Panic(err)
	}
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	seer := &SeerRun{}
	seer.Run()

	switch seer.TestIndicator {
	case TestPreExecutionLarge:
		experiments.TestPreExecutionLarge(seer.TxNum, seer.StartingBlock, seer.BlockNum, seer.DisorderRatio)
	case TestPrediction:
		experiments.TestPredictionSuccess(seer.StartingBlock, seer.BlockNum, 0)
	case TestSpeedup:
		experiments.TestSpeedupPerTx(seer.StartingBlock, seer.BlockNum, seer.DisorderRatio, seer.EnableRepair, seer.EnablePerceptron, seer.EnableFast)
	case TestConcurrent:
		experiments.TestSeerConcurrentLarge(seer.Threads, seer.TxNum, seer.StartingBlock, seer.BlockNum)
	case TestAbort:
		experiments.TestSeerConcurrentAbort(seer.Threads, seer.TxNum, seer.BlockNum)
	case TestBreakDown:
		experiments.TestSeerBreakDown(seer.StartingBlock, seer.BlockNum, seer.DisorderRatio, seer.EnableRepair, seer.EnablePerceptron, seer.EnableFast)
	case TestMemoryCostBaseline:
		experiments.TestMemoryBaseline(seer.StartingBlock, seer.BlockNum)
	case TestMemoryCost:
		experiments.TestMemoryBreakDown(seer.StartingBlock, seer.BlockNum, seer.EnablePerceptron, seer.EnableFast, seer.StoreCheckpoint)
	}
}
