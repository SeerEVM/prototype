## Description
The repository hosts the implementation of SeerEVM, an advanced transaction execution engine for Ethereum blockchain.

SeerEVM incorporates fine-grained branch prediction to fully exploit pre-execution effectiveness. Seer predicts state-related branches using a two-level prediction approach, reducing inconsistent execution paths more efficiently than executing all possible branches. To enable effective reuse of pre-execution results, Seer employs checkpoint-based fast-path execution, enhancing transaction execution for both successful and unsuccessful predictions.

## Precondition

**Testbeds:** 

- Amazon EC2 c7i.8xlagre instance (32 vCPUs, 64 GB memory), running Ubuntu 22.04 LTS

**Necessary software environment:**

- Installation of go (go version >= 1.20)

Remove any previous Go installation by deleting the /usr/local/go folder (if it exists), then extract the archive you just downloaded into /usr/local, creating a fresh Go tree in /usr/local/go:

```shell script
$ rm -rf /usr/local/go && tar -C /usr/local -xzf go1.22.1.linux-amd64.tar.gz
```

Add /usr/local/go/bin to the PATH environment variable.

```shell script
export PATH=$PATH:/usr/local/go/bin
```

- Install Go-related modules/packages

```shell script
# Under the current directory `SeerEVM`
go mod tidy
```

**Dataset:**

Due to the size of the dataset used in our paper exceeding 100 GB, we have trimmed the dataset to facilitate artifact evaluation. We provide two Ethereum datasets, each spanning 1000 blocks (including state data):

- Block height ranging from 14,000,000 ~ 14,001,000, shown in `./ethereumdata_1400w_small`
- Block height ranging from 14,650,000 ~ 14,651,000, shown in `./ethereumdata_1465w_small`

## Usage

Here, we show how to produce the experimental results shown in our paper step by step. All the test scripts are presented in the directory `./experiments`. Note that due to the different idle state of the machine's resources, it is normal for the reproducible results to have slight deviations. 

#### 1.1 Prediction accuracy

Enter the directory `./experiments` and run the script `prediction_height.sh`:

```shell script
cd experiments
./prediction_height.sh
```

This script run would output the prediction accuracy across 1,000 blocks shown in `prediction_height.txt`, which is corresponding to Figure 10 in the paper.

#### 1.2 Pre-execution latency

In the current directory, run the script `pre_execution_lat_large.sh`:

```shell script
./pre_execution_lat_large.sh
```

This script run would output the pre-execution latency under different number of transactions shown in `preExecution_large_ratio.txt`, which is corresponding to Figure 11 in the paper.

#### 1.3 Speedup over single transaction execution

In the current directory, run the script `speedup_tx.sh`:

```shell script
./speedup_tx.sh
```

This script run would output the speedup distribution across smart contract transactions shown in `speedup_perTx_full.txt`, which is corresponding to Figure 12 in the paper. Please note that we employ 10,000 blocks to evaluate such speedup performance in the paper. Here, we only employ 1,000 blocks for artifact evaluation. 

#### 1.4 Concurrent execution performance

In the current directory, run the script `con_execution_abort.sh`:

```shell script
./con_execution_abort.sh
```

This script run would output the abort rate under varying number of concurrent transactions shown in `concurrent_abort.txt`, which is corresponding to Table 2 in the paper. 

Then, run the script `con_execution_speedup.sh`:

```shell script
./con_execution_speedup.sh
```

This script run would output the speedup over serial execution by using different threads shown in `concurrent_speedup.txt`, which is corresponding to Figure 13 in the paper. 

#### 1.5 Design breakdown

To shorten the time to reproduce, all the evaluation scripts in this part only use 100 blocks to output the corresponding results.

In the current directory, run the script `design_breakdown.sh`:

```shell script
./design_breakdown.sh
```

This script run would output the prediction accuracy and the pre-execution latency per block for each variant, shown in `prediction_breakdown_$name.txt` and `preExecution_breakdown_$name.txt`, respectively, corresponding to Figure.14 (a) and (b) in the paper. `$name` refers to different variants of Seer, i.e., `basic`, `repair`, `perceptron`. 

To obtain the speedup performance under design breakdown, run the script `speedup_breakdown.sh`:

```shell script
./speedup_breakdown.sh
```

This script run would output the speedup over serial execution of different variants of Seer, shown in ``speedup_perTx_$name.txt`, corresponding to Figure.14 (c) in the paper. `$name` refers to different variants of Seer, i.e., `basic`, `repair`, `perceptron`, `full`. 

#### 1.6 Memory cost

In the current directory, run the script `memory_test.sh`:

```shell script
./memory_test.sh
```

Meanwhile, open anther terminal window and run the script `memory_monitor.sh` to monitor the memory usage:

```shell script
./memory_monitor.sh
```

 The script `memory_test.sh` contains four rounds of evaluations for `baseline`, `Seer-basic`, `Seer-perceptron`, and `Seer`. The script `memory_monitor.sh` would end when each variant's memory test is completed. Hence,  when the memory test for each variant is completed, restart the run of the script `./memory_monitor.sh`. The memory overhead result is shown in `memory_usage.txt`, which is corresponding to Figure.14 (d) in the paper.
