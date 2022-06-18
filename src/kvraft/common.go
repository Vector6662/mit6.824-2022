package kvraft

import (
	"log"
	"time"
)

const Debug = false

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

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

func ExecutionTimeOut() time.Duration {
	return 500 * time.Millisecond
}

type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	ClientId    int64
	SequenceNum int64
	Key         string
	Value       string
	Method      string
}

type Session struct {
	ClientId          int64
	LatestSequenceNum int64
	Reply             Reply
}
