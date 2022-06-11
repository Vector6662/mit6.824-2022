package kvraft

import (
	"6.824/labrpc"
	"sync/atomic"
	"time"
)
import "crypto/rand"
import "math/big"

type Clerk struct {
	servers []*labrpc.ClientEnd
	// You will have to modify this struct.
	leaderId    int64
	clientId    int64
	sequenceNum int64
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	ck.sequenceNum = 0
	// You'll have to add code here.
	ck.clientId = nrand()
	return ck
}

// Get
// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.Get", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//
func (ck *Clerk) Get(key string) string {

	// You will have to modify this function.
	sequenceNum := atomic.LoadInt64(&ck.sequenceNum)
	args := &Args{
		ClientId:    ck.clientId,
		SequenceNum: sequenceNum,
		Key:         key,
		Method:      GET,
	}
	return ck.process(args).Value
}

func (ck *Clerk) Put(key string, value string) {
	sequenceNum := atomic.LoadInt64(&ck.sequenceNum)
	args := &Args{
		ClientId:    ck.clientId,
		SequenceNum: sequenceNum,
		Key:         key,
		Value:       value,
		Method:      PUT,
	}
	ck.process(args)
}
func (ck *Clerk) Append(key string, value string) {
	sequenceNum := atomic.LoadInt64(&ck.sequenceNum)
	args := &Args{
		ClientId:    ck.clientId,
		SequenceNum: sequenceNum,
		Key:         key,
		Value:       value,
		Method:      APPEND,
	}
	ck.process(args)
}

func (ck *Clerk) process(args *Args) Reply {
	//todo 一个client目前的实现是串行发送给server，当前的请求必须得到回复才能够进行下一个请求，
	// 所以应该是不需要保证原子性的
	leaderId := atomic.LoadInt64(&ck.leaderId)
	for {
		reply := &Reply{}
		start := time.Now()
		ok := ck.servers[leaderId].Call("KVServer.RPC", args, reply)
		DPrintf("【SEND】client(%v), leader(%v)  args:%v ok:%v, reply(%v), dur:%v",
			ck.clientId, leaderId, args, ok, reply, time.Since(start))

		if !ok || ErrWrongLeader == reply.Err || ErrTimeOut == reply.Err {
			leaderId = (leaderId + 1) % int64(len(ck.servers))
			time.Sleep(30 * time.Millisecond)
			continue
		}
		atomic.AddInt64(&ck.sequenceNum, 1)
		atomic.StoreInt64(&ck.leaderId, leaderId)
		return *reply
	}
}
