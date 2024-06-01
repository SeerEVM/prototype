## Description
The repository hosts the implementation of SeerEVM, an advanced transaction execution engine for Ethereum blockchain.

SeerEVM incorporates fine-grained branch prediction to fully exploit pre-execution effectiveness. Seer predicts state-related branches using a two-level prediction approach, reducing inconsistent execution paths more efficiently than executing all possible branches. To enable effective reuse of pre-execution results, Seer employs checkpoint-based fast-path execution, enhancing transaction execution for both successful and unsuccessful predictions.


## Usage


### 1. Precondition
Necessary software environment:
- Installation of go

Remove any previous Go installation by deleting the /usr/local/go folder (if it exists), then extract the archive you just downloaded into /usr/local, creating a fresh Go tree in /usr/local/go:
```shell script
$ rm -rf /usr/local/go && tar -C /usr/local -xzf go1.22.1.linux-amd64.tar.gz
```

Add /usr/local/go/bin to the PATH environment variable.
```shell script
export PATH=$PATH:/usr/local/go/bin
```

### 2. Steps to run SeerEVM
#### 2.1 Install fabric on the work computer

- installation of fabric

Fabric is best installed via pip:
``` shell script
pip install fabric
```


#### 2.2 Login replicas without passwords 
Enable workcomputer to login in servers and clients without passwords.

Commands below are run on the *work computer*.
```shell script
# Generate the ssh keys (if not generated before)
ssh-keygen
ssh-copy-id -i ~/.ssh/id_rsa.pub $IP_ADDR_OF_EACH_SERVER
```

#### 2.3 Install Go-related modules/packages

```shell
# Enter the directory `go-MorphDAG`
go mod tidy
```

