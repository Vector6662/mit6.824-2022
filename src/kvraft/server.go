package kvraft

import (
	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

const Debug = false

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
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
	clientId          int64
	latestSequenceNum int64
	reply             Reply
}

type KVServer struct {
	mu      sync.RWMutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

	maxraftstate int // snapshot if log grows this big

	// Your definitions here.
	clientSession map[int64]Session
	data          Data
	notifyChanMap map[int]chan Reply
}

func (kv *KVServer) applier() {
	for !kv.killed() {
		applyMsg := <-kv.applyCh
		if applyMsg.CommandValid {
			var reply Reply
			commandIndex, op := applyMsg.CommandIndex, applyMsg.Command.(Op)

			kv.mu.Lock()
			if GET != op.Method && kv.isDuplicate(op.ClientId, op.SequenceNum) {
				reply = kv.clientSession[op.ClientId].reply
			} else {
				switch op.Method {
				case PUT:
					reply.Err = kv.data.put(op.Key, op.Value)
				case APPEND:
					reply.Err = kv.data.append(op.Key, op.Value)
				case GET:
					reply.Value, reply.Err = kv.data.get(op.Key)
				}
				if GET != op.Method {
					kv.clientSession[op.ClientId] = Session{
						clientId:          op.ClientId,
						latestSequenceNum: op.SequenceNum,
						reply:             reply,
					}
				}
			}
			if currentTerm, isLeader := kv.rf.GetState(); isLeader && currentTerm == applyMsg.CommandTerm {
				kv.notifyChanMap[commandIndex] <- reply
			}
			kv.mu.Unlock()
		} else if applyMsg.SnapshotValid {
			// todo snapshot

		}
	}
}

func (kv *KVServer) RPC(args *Args, reply *Reply) {
	// todo 我自己的逻辑一直有bug，但我还没找到是啥问题。下边的代码参照了这里的逻辑：https://github.com/OneSizeFitsQuorum/MIT6.824-2021/blob/master/docs/lab3.md
	//
	kv.mu.Lock()
	if GET != args.Method && kv.isDuplicate(args.ClientId, args.SequenceNum) {
		r := kv.clientSession[args.ClientId].reply
		reply.Value, reply.Err = r.Value, r.Err
		kv.mu.Unlock()
		return
	}
	kv.mu.Unlock()

	op := Op{ //感觉这么做duck不必啊，反正字段和args都一毛一样
		ClientId:    args.ClientId,
		SequenceNum: args.SequenceNum,
		Key:         args.Key,
		Value:       args.Value,
		Method:      args.Method,
	}
	// todo $8提到了实现读一致性second，需要保证自己仍然是leader才能处理读请求
	index, _, isLeader := kv.rf.Start(op)
	if !isLeader {
		reply.Err = ErrWrongLeader
		return
	}
	DPrintf("start execute(leader:%v, index:%v)", kv.me, index)
	notifyChan := make(chan Reply, 1)
	kv.mu.Lock()
	kv.notifyChanMap[index] = notifyChan
	kv.mu.Unlock()

	select {
	case r := <-notifyChan:
		reply.Value, reply.Err = r.Value, r.Err
	case <-time.After(ExecutionTimeOut()):
		go func() {
			<-notifyChan
		}()
		reply.Err = ErrTimeOut
	}
}


func (kv *KVServer) isDuplicate(clientId, sequenceNum int64) bool {
	session, ok := kv.clientSession[clientId]
	if !ok {
		return false
	}
	return session.latestSequenceNum == sequenceNum
}

// Kill
// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
//
func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

// StartKVServer
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
//
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Op{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate

	// You may need initialization code here.

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)

	// You may need initialization code here.
	kv.data = make(map[string]string)
	kv.notifyChanMap = make(map[int]chan Reply)
	kv.clientSession = make(map[int64]Session)

	go kv.applier()

	return kv
}

// Data KV数据库
type Data map[string]string

func (data Data) get(key string) (string, Err) {
	val, ok := data[key]
	if !ok {
		return "", ErrNoKey
	}
	return val, OK
}
func (data Data) put(key, val string) Err {
	data[key] = val
	return OK
}
func (data Data) append(key, val string) Err {
	data[key] += val
	return OK
}
