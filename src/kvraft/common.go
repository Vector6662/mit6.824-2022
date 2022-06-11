package kvraft

import "time"

const (
	OK             = "OK"
	ErrNoKey       = "ErrNoKey"
	ErrWrongLeader = "ErrWrongLeader"
	ErrTimeOut     = "ErrTimeOut"
	GET            = "GET"
	APPEND         = "APPEND"
	PUT            = "PUT"
)

type Err string

// Put or Append
type Args struct {
	ClientId    int64
	SequenceNum int64
	Key         string
	Value       string
	Method      string
}

type Reply struct {
	Value string
	Err   Err
}

func RPCTimeOut() time.Duration {
	return 500 * time.Millisecond
}

func ExecutionTimeOut() time.Duration {
	return 500 * time.Millisecond
}
