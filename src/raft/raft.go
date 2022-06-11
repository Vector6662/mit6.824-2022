package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"6.824/labgob"
	"bytes"
	"context"
	"fmt"
	//	"bytes"
	"sync"
	"sync/atomic"
	"time"

	//	"6.824/labgob"
	"6.824/labrpc"
)

// ApplyMsg
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in part 2D you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh, but set CommandValid to false for these
// other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int
	CommandTerm  int

	// For 2D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}
type Entry struct {
	Index   int
	Term    int
	Command interface{}
}

var emptyEntry = Entry{
	Term:  0,
	Index: 0,
}

type Log struct {
	entries []Entry
	mu      sync.RWMutex
}

// return lastIndex,lastTerm
func (l *Log) lastEntry() *Entry {
	if len(l.entries) == 0 {
		_ = fmt.Errorf("【entry err】len=0")
		return &Entry{
			Term:  0,
			Index: 0,
		}
	}
	return &l.entries[len(l.entries)-1]
}
func (l *Log) firstEntry() *Entry {
	if len(l.entries) == 0 {
		_ = fmt.Errorf("【entry err】len=0")
		return &Entry{
			Term:  0,
			Index: 0,
		}
	}
	return &l.entries[0]
}

func (l *Log) appendOne(entry Entry) {
	l.entries = append(l.entries, entry)
}
func (l *Log) appendAll(entries []Entry) {
	l.entries = append(l.entries, entries...)
}

// 检查新的entry能否加入到log中
func (l *Log) checkEntry(entry *Entry) bool {
	lastEntry := l.lastEntry()
	if entry.Term < lastEntry.Term {
		DPrintf("【ERROR】log: cannot append entry(term=%v index=%v) with earlier term. lastEntry(term=%v, index=%v)",
			entry.Term, entry.Index, lastEntry.Term, lastEntry.Index)
		return false
	}
	if entry.Term == lastEntry.Term && entry.Index <= lastEntry.Index { //entry的index必须比lastEntry的大
		DPrintf("【ERROR】log: cannot append entry(term=%v index=%v) with earlier index with same term. lastEntry(term=%v, index=%v)",
			entry.Term, entry.Index, lastEntry.Term, lastEntry.Index)
		return false
	}
	// 应该不会出现entry.Term>lastEntry.Term，且entry.Index <= lastEntry.Index的情况，在一个term下index只可能递增
	return true
}

const (
	Follower  = 1
	Leader    = 2
	Candidate = 3
)

// Raft
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.RWMutex        // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	state int
	//Persistent state on all servers
	currentTerm int
	votedFor    int // 每一个term只能投一次票
	log         Log
	//Volatile state on all servers
	commitIndex int
	lastApplied int
	//Volatile state on leaders
	nextIndex  []int
	matchIndex []int

	electionTimer  *time.Timer
	heartbeatTimer *time.Timer
	notifyApplyCh  chan int //这是一个比较好的优化，backup测试从45s降低至35s
	applyCh        chan ApplyMsg
}

// GetState return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	// Your code here (2A).
	//rf.mu.RLock()
	//defer rf.mu.RUnlock()

	return rf.currentTerm, rf.state == Leader
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	rf.persister.SaveRaftState(rf.encode())
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		rf.log.appendOne(emptyEntry)
		return
	}
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var currentTerm, votedFor int
	var entries []Entry
	e1, e2, e3 := d.Decode(&currentTerm), d.Decode(&votedFor), d.Decode(&entries)
	if e1 != nil || e2 != nil || e3 != nil {
		_ = fmt.Errorf("【Decode err, e1:%v, e2:%v, e3:%v】", e1, e2, e3)
	} else {
		rf.currentTerm, rf.votedFor, rf.log.entries = currentTerm, votedFor, entries
		if len(entries) == 0 {
			rf.log.appendOne(emptyEntry)
		}
		//细节，在reboot之后也是需要初始化的两个变量，这两个变量属于Volatile state on all servers
		//忽略了这个，让我在TestSnapshotInstallCrash2D没有通过
		rf.commitIndex, rf.lastApplied = rf.log.firstEntry().Index, rf.log.firstEntry().Index
	}
}

func (rf *Raft) encode() []byte {
	w := &bytes.Buffer{}
	e := labgob.NewEncoder(w)
	entries := make([]Entry, len(rf.log.entries))
	copy(entries, rf.log.entries)
	if e.Encode(rf.currentTerm) != nil || e.Encode(rf.votedFor) != nil || e.Encode(entries) != nil {
		_ = fmt.Errorf("【Encode err】")
	}
	return w.Bytes()
}

// CondInstallSnapshot
// A service wants to switch to snapshot.  Only do so if Raft hasn't
// have more recent info since it communicate the snapshot on applyCh.
//
func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {

	// Your code here (2D).

	return true
}

// Snapshot the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).
	rf.mu.Lock()
	defer rf.mu.Unlock()

	firstIndex := rf.log.firstEntry().Index
	if index <= firstIndex {
		return
	}

	rf.log.entries = rf.log.entries[index-firstIndex:]
	rf.log.entries[0].Command = nil // 单纯为了节约空间
	rf.persister.SaveStateAndSnapshot(rf.encode(), snapshot)
}
func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	//log
	{
		DPrintf("##############################【InstallSnapshot】##############################")
		DPrintf("(peer=%v term=%v,lastApplied=%v,commitIndex=%v)", rf.me, rf.currentTerm, rf.lastApplied, rf.commitIndex)
		DPrintf("args(LastIncludedIndex:%v, LastIncludedTerm=%v)", args.LastIncludedIndex, args.LastIncludedTerm)
		DPrintf("【现有entries】:%v", rf.log.entries)
		defer DPrintf("##############################################################################")
	}

	reply.Term = rf.currentTerm
	if args.Term < rf.currentTerm {
		reply.Message = Format("leader with earlier term:%v", args.Term)
		return
	}
	if args.Term > rf.currentTerm {
		rf.currentTerm, rf.votedFor = args.Term, -1
		rf.persist()
	}
	rf.changeState(Follower)
	rf.electionTimer.Reset(ElectionTimeOut())

	//此处参考：https://github.com/OneSizeFitsQuorum/MIT6.824-2021/blob/master/docs/lab2.md#%E6%97%A5%E5%BF%97%E5%8E%8B%E7%BC%A9
	if args.LastIncludedIndex <= rf.commitIndex {
		reply.Message = Format("Don't need snapshot. args.LastIncludedIndex(%v)<=rf.commitIndex(%v)", args.LastIncludedIndex, rf.commitIndex)
		return
	}
	if args.LastIncludedIndex > rf.log.lastEntry().Index {
		//占据entries切片中下标为0的位置，始终得保持entries不为空
		rf.log.entries = make([]Entry, 1)
	} else {
		rf.log.entries = rf.log.entries[args.LastIncludedIndex-rf.log.firstEntry().Index:]
	}
	rf.log.entries[0].Index, rf.log.entries[0].Term, rf.log.entries[0].Command =
		args.LastIncludedIndex, args.LastIncludedTerm, nil

	rf.commitIndex, rf.lastApplied = args.LastIncludedIndex, args.LastIncludedIndex
	rf.persister.SaveStateAndSnapshot(rf.encode(), args.Data)

	reply.Message = Format("firstEntry:%v", rf.log.firstEntry())

	//apply
	go func() {
		rf.applyCh <- ApplyMsg{
			CommandValid:  false,
			SnapshotValid: true,
			Snapshot:      args.Data,
			SnapshotTerm:  args.LastIncludedTerm,
			SnapshotIndex: args.LastIncludedIndex,
		}
	}()
}

type AppendEntriesArgs struct {
	Term         int
	LeaderId     int
	PreLogIndex  int
	PreLogTerm   int
	Entries      []Entry //empty for heartbeat
	LeaderCommit int     //leader's CommitIndex
}

// AppendEntriesReply 每一个RPC请求必须用不同的reply，否则labgob会有警告(labgob:169)：labgob warning: Decoding into a ...
type AppendEntriesReply struct {
	Term      int
	NextIndex int
	Success   bool
	Message   string
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()

	//log
	{
		DPrintf("++++++++++++++++++++++++++++++【AppendEntries】+++++++++++++++++++++++++++++++")
		DPrintf("(peer=%v term=%v,lastApplied=%v,commitIndex=%v)",
			rf.me, rf.currentTerm, rf.lastApplied, rf.commitIndex)
		defer DPrintf("+++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++")
	}

	// 2A
	if args.Term < rf.currentTerm { // <
		reply.Success, reply.Term = false, rf.currentTerm
		reply.NextIndex = rf.log.lastEntry().Index + 1 //最开始想的是直接返回-1，这样容易给leader带来bug，
		reply.Message = Format("leader with earlier term:%v", args.Term)
		return
	}

	// args.Term > rf.currentTerm
	// 这里有个细节需要注意一下，当term == rf.CurrentTerm时，有可能的状态是当前(rf)是Candidate，
	// 然而AppendEntries的调用者已经发出了该调用，说明他已经赢得了选举，此时rf就应该切换成为Follower
	// 换句话说，在 >= 的情况下当前rf必定得切换为Follower，不管他以前是什么身份
	rf.changeState(Follower)
	rf.electionTimer.Reset(ElectionTimeOut())

	if args.Term > rf.currentTerm {
		rf.votedFor, rf.currentTerm = -1, args.Term
	}

	// 2B

	DPrintf("【现有entries】:%v", rf.log.entries)
	if len(args.Entries) != 0 { //just heartbeat
		DPrintf("【receive entries】entries(%v)", args.Entries)
	} else {
		DPrintf("just heartbeat")
	}

	//考察一下能否取等？应该是不，相等的时候也不耽误后边的truncate，因为不会truncate到commitIndex这个点
	if args.PreLogIndex < rf.commitIndex {
		reply.Success, reply.Term = false, rf.currentTerm
		reply.NextIndex = rf.commitIndex + 1
		reply.Message = Format("【WARN】Index already committed: commitIndex=%v args(preLogIndex=%v, preLogTerm=%v)",
			rf.commitIndex, args.PreLogIndex, args.PreLogTerm)
		return
	}

	firstIndex, lastIndex := rf.log.firstEntry().Index, rf.log.lastEntry().Index
	if args.PreLogIndex > lastIndex {
		reply.Success, reply.Term = false, rf.currentTerm
		reply.NextIndex = lastIndex + 1
		reply.Message = Format("【WARN】Entry Index doesn't exist lastIndex=%v: args(PreLogIdx=%v, PreLogTerm=%v)",
			rf.log.lastEntry().Index, args.PreLogIndex, args.PreLogTerm)
		return
	}
	if args.PreLogIndex < firstIndex {
		rf.log.appendOne(emptyEntry)
	} else if len(rf.log.entries) > 0 { // preLogIndex在[commitIndex,lastIndex]区间
		position := args.PreLogIndex - firstIndex
		entry := rf.log.entries[position] //冲突的entry

		if entry.Term != args.PreLogTerm { // len的判断不是很懂存在的必要
			nextIndex, conflictTerm := entry.Index-1, entry.Term
			//nextIndex,这会加快TestBackup2B(),55s->50s
			for ; nextIndex >= firstIndex; nextIndex-- {
				if rf.log.entries[nextIndex-firstIndex].Term != conflictTerm {
					break
				}
			}
			reply.Success, reply.Term = false, rf.currentTerm
			reply.NextIndex = nextIndex + 1
			reply.Message = Format("【WARN】entry at index %v doesn't match term %v: args(PreLogIdx=%v, PreLogTerm=%v)",
				entry.Index, entry.Term, args.PreLogIndex, args.PreLogTerm)
			return
		}

		if args.PreLogIndex < lastIndex { //若是等于，就不用truncate了，相当于简化了一下分支操作，这很细节
			//truncate
			rf.log.entries = rf.log.entries[firstIndex-firstIndex : position+1] //容易失误的点，写成：[firstIndex : position+1]，firstIndex并不是entries的索引
		}
	}

	//写args中的entry
	for _, entry := range args.Entries {
		if !rf.log.checkEntry(&entry) {
			reply.Success, reply.Term = false, rf.currentTerm
			return
		}
	}
	rf.log.appendAll(args.Entries)

	// 原来要在万事俱备的时候才能够更新commitIndex，我之前把这段代码放在了2B代码的最开始，但是有可能会有这样的冲突：
	// leader=0,entries=[(1,1,101),(2,2,103)] 而follower=2,entries=[(1,1,101),(2,2,102)]，也就是说，follower=2在并没有
	// 和leader=0的所有entries“拉通对齐”的时候就根据args.LeaderCommit更新了自己的commitIndex，然后applier协程apply

	// 这个错误其实不应该犯，因为在论文的figure 2中AppendEntries RPC已经描述了操作顺序，更新commitIndex是在第五个也就是最后一个
	if args.LeaderCommit > rf.commitIndex {
		rf.commitIndex = Min(args.LeaderCommit, rf.log.lastEntry().Index)
	}

	reply.Success, reply.Term = true, rf.currentTerm
	reply.NextIndex = rf.log.lastEntry().Index + 1
	// 尽快释放锁，用协程通知apply
	go func() {
		rf.notifyApplyCh <- rf.commitIndex
	}()
}

// RequestVoteArgs
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term         int // candidate term
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

// RequestVoteReply
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term        int  //currentTerm, for candidate to update itself
	VoteGranted bool //true means candidate received vote
	Message     string
}

type InstallSnapshotArgs struct {
	Term              int
	LeaderId          int
	LastIncludedIndex int
	LastIncludedTerm  int
	Offset            int
	Data              []byte
	Done              bool
}

type InstallSnapshotReply struct {
	Term    int
	Message string
}

// RequestVote
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	//Your code here (2A, 2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist() //两个地方要改变currentTerm和voteFor，干脆就在这里统一处理了

	//log
	{
		DPrintf("*******************************【RequestVote】*********************************")
		DPrintf("peer=%v,term=%v,lastApplied=%v,commitIndex=%v  candidate(peer=%v, term=%v)",
			rf.me, rf.currentTerm, rf.lastApplied, rf.commitIndex, args.CandidateId, args.Term)
		defer DPrintf("******************************************************************************")
	}

	//2A
	if args.Term < rf.currentTerm { // <
		reply.VoteGranted, reply.Term = false, rf.currentTerm
		reply.Message = Format("candidate的term小了")
		return
	}
	if args.Term == rf.currentTerm && rf.votedFor != -1 && rf.votedFor != args.CandidateId { // == 表明没投给当前Candidate，投给了别人或者自己
		reply.VoteGranted, reply.Term = false, rf.currentTerm
		reply.Message = Format("当前term没投给这个Candidate，投给了自己或则别人")
		return
	}

	if args.Term > rf.currentTerm { // >
		rf.currentTerm, rf.votedFor = args.Term, -1
		rf.changeState(Follower)
	}

	//2B
	// 满足"至少不落后"语义 $5.4.1
	lastEntry := rf.log.lastEntry()
	lastIndex, lastTerm := lastEntry.Index, lastEntry.Term
	//之前看了etcd的RequestVote实现源码，感觉不是很对呢：http://arthurchiao.art/blog/raft-paper-zh/#503-requestvote-rpc
	if lastTerm > args.LastLogTerm || (lastTerm == args.LastLogTerm && lastIndex > args.LastLogIndex) {
		reply.VoteGranted, reply.Term = false, rf.currentTerm
		reply.Message = Format("不满足$5.4.1   lastEntry(%v,%v) args.last(%v,%v)",
			lastIndex, lastTerm, args.LastLogIndex, args.LastLogTerm)
		return
	}

	//此时rf没有投票(==-1)，或者已经投给了candidateId，说明这个时候就是一个follower
	//DPrintf("【RPC:RequestVote】peer:%v votes:%v.  term:%v", rf.me, args.CandidateId, args.Term)
	rf.votedFor = args.CandidateId
	rf.electionTimer.Reset(ElectionTimeOut()) // 投票之后才重置选举超时时间
	reply.VoteGranted, reply.Term = true, rf.currentTerm
	reply.Message = "Grant Vote!"
}

// Start
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	// Your code here (2B).
	if Leader != rf.state {
		return -1, rf.currentTerm, false
	}

	index := rf.log.lastEntry().Index + 1
	entry := Entry{
		Term:    rf.currentTerm,
		Index:   index,
		Command: command,
	}
	//不必进行checkEntry

	rf.log.appendOne(entry)
	rf.persist()
	DPrintf("<><><>Leader Receive Entry<><><>  leader(me=%v,state=%v,term=%v) entry(index=%v,term=%v,command=%v)",
		rf.me, rf.state, rf.currentTerm, entry.Index, entry.Term, entry.Command)

	// 应该立即发送一轮广播，否则在Lab 3A的TestSpeed3A会超时，报错：test_test.go:421: Operations completed too slowly 129.23ms/op > 33.333333ms/op
	// 这个时间和我设置的一轮heartbeat的时间相近
	// todo 立马heartbeat似乎不是最好的方式，我觉得应该等待大致30ms后，这样可以累计一定的entry后再进行同步
	go rf.broadcastHeartbeat()

	return entry.Index, entry.Term, true
}

// Kill
// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
//
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

// 执行此方法上下文应该要有锁
func (rf *Raft) broadcastHeartbeat() {

	rf.mu.Lock()
	defer rf.mu.Unlock()

	heartbeatTimeout := HeartbeatTimeOut()
	heartbeatCtx, cancelFunc := context.WithTimeout(context.Background(), heartbeatTimeout)

	rf.heartbeatTimer.Reset(heartbeatTimeout)

	//log
	{
		DPrintf("")
		DPrintf("XXXXXXXXXXXXXXXXXXXXXXXXXXXXXX【BROADCAST HEARTBEAT】XXXXXXXXXXXXXXXXXXXXXXXXXXXXXX")
		DPrintf("leader=%v,term=%v, lastApplied:%v, leaderCommit=%v", rf.me, rf.currentTerm, rf.lastApplied, rf.commitIndex)
		DPrintf("【现有entries】:%v", rf.log.entries)
		for peer := range rf.peers {
			DPrintf("peer=%v matchIndex=%v  nextIndex=%v", peer, rf.matchIndex[peer], rf.nextIndex[peer])
		}
		DPrintf("XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX")
		DPrintf("")
	}

	for peer := range rf.peers {
		if peer == rf.me {
			continue
		}
		go rf.sendAppendEntries(peer, heartbeatCtx, cancelFunc)
	}
}

// 应该持有锁
func (rf *Raft) startElection() {
	rf.mu.Lock()

	electionTimeout := ElectionTimeOut()
	electionCtx, cancelFunc := context.WithTimeout(context.Background(), electionTimeout)

	rf.electionTimer.Reset(electionTimeout)
	defer cancelFunc()

	rf.currentTerm, rf.votedFor = rf.currentTerm+1, rf.me
	rf.persist()
	rf.changeState(Candidate)
	args := rf.newRequestVoteArgs()
	rf.mu.Unlock()

	//log
	{
		DPrintf("")
		DPrintf("$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$【Election Start】$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$4")
		DPrintf("candidate:%v, term:%v,  lastApplied=%v, commitIndex=%v, entries(%v)",
			rf.me, args.Term, rf.lastApplied, rf.commitIndex, rf.log.entries)
		DPrintf("$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$")
		DPrintf("")
	}

	voteCh := make(chan bool, len(rf.peers)) // 采用缓冲通道，这样sendRequestVote协程们便不会阻塞

	for peer := range rf.peers {
		if peer == rf.me {
			continue
		}
		go rf.sendRequestVote(peer, args, voteCh, electionCtx, cancelFunc)
	}

	var votes = 1
	for range rf.peers {
		select {
		case voteGranted := <-voteCh:
			if !voteGranted {
				continue
			}
		case <-electionCtx.Done():
			return
		}
		votes++
		if votes > len(rf.peers)/2 && Candidate == rf.state { // 选举成功，广而告之
			cancelFunc()
			DPrintf("【NEW LEADER】Leader:%v Term: %v", args.CandidateId, args.Term)

			rf.mu.Lock()
			rf.changeState(Leader)

			// 初始化leader
			//注意一个细节，要在初始化完成后才能broadcast，否则会出现计算PreLogIndex的时候出现访问越界(-1)，
			//因为preLogIndex=nextIndex-1，若nextIndex=0，即默认值，就会出现上边的情况
			for peer := range rf.peers {
				rf.nextIndex[peer] = rf.log.lastEntry().Index + 1
				rf.matchIndex[peer] = rf.log.firstEntry().Index
			}
			go rf.broadcastHeartbeat()
			rf.mu.Unlock()
			return
		}
	}
}

//go routine
func (rf *Raft) sendAppendEntries(peer int, ctx context.Context, cancelFunc context.CancelFunc) {
	// 在每次send AppendEntries之前必须验证firstIndex，必须要大于nextIndex。
	// 一方面是因为在newAppendEntriesArgs方法中发送的entries的范围是[nextIndex-firstIndex : ]，
	// 另一方面，如果follower的nextIndex <= firstIndex，即这个follower还没有catch up leader的snapshot，
	// 则leader需要发送snapshot，先让这个follower跟上节奏
	if rf.nextIndex[peer] <= rf.log.firstEntry().Index {
		go rf.sendInstallSnapshot(peer, ctx, cancelFunc)
		return
	}
	rf.mu.RLock()
	args, _ := rf.newAppendEntriesArgs(peer) // 细节，这两个变量得放在协程里边，不然args会串味：args是个指针，存的是地址，下一个peer也会改变这个args， 然而每个peer的args是私人定制的
	rf.mu.RUnlock()
	reply := &AppendEntriesReply{}

	okCh := make(chan bool, 1)
	go func() {
		okCh <- rf.peers[peer].Call("Raft.AppendEntries", args, reply)
		close(okCh) //It should be executed only by the sender, never the receiver
	}()

	//借鉴labrpc.go 251行
	select {
	case ok := <-okCh:
		if !ok {
			DPrintf("【RPC false: AppendEntries】(peer=%v,term=%v,nextIndex=%v) leader args(peer=%v, term=%v)", peer, reply.Term, reply.NextIndex, args.LeaderId, args.Term)
			return
		}
	case <-ctx.Done():
		go func() {
			DPrintf("【RPC ctx Done: AppendEntries】(peer=%v,term=%v,nextIndex=%v) leader args(peer=%v, term=%v)", peer, reply.Term, reply.NextIndex, args.LeaderId, args.Term)
			<-okCh // drain channel to let the goroutine created earlier terminate
		}()
		return
	}

	//log
	{
		DPrintf("--------------------【Reply：AppendEntries:%v】------------------------", reply.Success)
		DPrintf("(peer=%v,term=%v,nextIndex=%v) leader args(peer=%v, term=%v)",
			peer, reply.Term, reply.NextIndex, args.LeaderId, args.Term)
		if reply.Message != "" {
			DPrintf("Message:%v", reply.Message)
		}
		DPrintf("---------------------------------------------------------------------")
	}

	// 2B
	rf.mu.Lock()
	defer rf.mu.Unlock()
	//这里发现一个漏洞，如果在获取锁之后，此时并不是leader了，那么会发生的情况是：
	//由于sendAppendEntries是并发的，前边的并发调用的该方法将当前rf从leader->follower，
	//并且term已经修改到了最新的，那么下边的方法都是以拥有最近term的follower的身份执行。
	//若此时reply.Success为false，且reply.Term == rf.currentTerm（reply的那个节点同样拥有最新的term），
	//那么将会再次调用sendAppendEntries
	if Leader != rf.state { //hold invariants
		return
	}
	if reply.Success { //true
		if reply.NextIndex > rf.nextIndex[peer] {
			rf.nextIndex[peer] = reply.NextIndex
			rf.matchIndex[peer] = reply.NextIndex - 1 // 提交成功了才会更新matchIndex
		}
		rf.updateCommitIndex()
	} else { //false
		if reply.Term > rf.currentTerm {
			cancelFunc()
			rf.changeState(Follower)
			// 注意还要改变votedFor=-1!!!这是一个大坑！！！
			// 表明已经知晓现在的任期号增加了，但是谁是该任期的leader还需要等待投票决定
			rf.currentTerm, rf.votedFor = reply.Term, -1
			rf.persist()
			return
		}
		rf.nextIndex[peer] = reply.NextIndex
		go rf.sendAppendEntries(peer, ctx, cancelFunc)
	}
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(peer int, args *RequestVoteArgs, voteCh chan<- bool, ctx context.Context, cancelFunc context.CancelFunc) {
	reply := &RequestVoteReply{} //每一个RPC请求必须用不同的reply，否则labgob会有警告(labgob:169)：labgob warning: Decoding into a ...

	okCh := make(chan bool, 1) //带缓冲的通道，这样在写入okCh后该协程便可以结束，不会有阻塞问题
	go func() {
		okCh <- rf.peers[peer].Call("Raft.RequestVote", args, reply)
		close(okCh) //It should be executed only by the sender, never the receiver
	}()

	//借鉴labrpc.go 257行
	select {
	case ok := <-okCh:
		if !ok {
			DPrintf("【RPC false：RequestVote】peer=%v,term=%v candidate(peer=%v,term=%v,state=%v)", peer, reply.Term, rf.me, args.Term, rf.state)
			voteCh <- false
			return
		}
	case <-ctx.Done():
		DPrintf("【RPC ctx Done：RequestVote】peer=%v,term=%v candidate(peer=%v,term=%v,state=%v)", peer, reply.Term, rf.me, args.Term, rf.state)
		voteCh <- false
		go func() {
			<-okCh // drain channel to let the goroutine created earlier terminate
		}()
		return
	}

	//log
	{
		DPrintf("--------------------【Reply：RequestVote:%v】---------------------------", reply.VoteGranted)
		DPrintf("peer=%v,term=%v candidate(peer=%v,term=%v,state=%v)",
			peer, reply.Term, rf.me, args.Term, rf.state)
		if reply.Message != "" {
			DPrintf("Message:%v", reply.Message)
		}
		DPrintf("-----------------------------------------------------------------------")
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()
	if Candidate != rf.state {
		return
	}
	if reply.VoteGranted {
		voteCh <- true
	} else if reply.Term > rf.currentTerm {
		cancelFunc()
		voteCh <- false
		rf.changeState(Follower)
		rf.currentTerm, rf.votedFor = reply.Term, -1
		rf.persist()
	}
}

func (rf *Raft) sendInstallSnapshot(peer int, ctx context.Context, cancelFunc context.CancelFunc) {
	rf.mu.RLock()
	args := &InstallSnapshotArgs{
		Term:              rf.currentTerm,
		LeaderId:          rf.me,
		LastIncludedIndex: rf.log.firstEntry().Index,
		LastIncludedTerm:  rf.log.firstEntry().Term,
		Data:              rf.persister.ReadSnapshot(),
	}
	rf.mu.RUnlock()
	reply := &InstallSnapshotReply{}
	okCh := make(chan bool, 1)
	go func() {
		okCh <- rf.peers[peer].Call("Raft.InstallSnapshot", args, reply)
		close(okCh)
	}()
	select {
	case ok := <-okCh:
		if !ok {
			DPrintf("【RPC false: InstallSnapshot】(peer=%v,term=%v) leader args(peer=%v, term=%v,LastIncludedIndex=%v,LastIncludedTerm=%v)",
				peer, reply.Term, args.LeaderId, args.Term, args.LastIncludedIndex, args.LastIncludedTerm)
			return
		}
	case <-ctx.Done():
		go func() {
			DPrintf("【RPC Done: InstallSnapshot】(peer=%v,term=%v) leader args(peer=%v, term=%v,LastIncludedIndex=%v,LastIncludedTerm=%v)",
				peer, reply.Term, args.LeaderId, args.Term, args.LastIncludedIndex, args.LastIncludedTerm)
			<-okCh
		}()
		return
	}

	//log
	{
		DPrintf("^^^^^^^^^^^^^^^^^^^^^【Reply：InstallSnapshot】^^^^^^^^^^^^^^^^^^^^^^^^^")
		DPrintf("(peer=%v,term=%v) leader args(peer=%v, term=%v)",
			peer, reply.Term, args.LeaderId, args.Term)
		if reply.Message != "" {
			DPrintf("Message:%v", reply.Message)
		}
		DPrintf("^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^")
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()
	if Leader != rf.state { //hold invariant
		return
	}
	if reply.Term > rf.currentTerm {
		cancelFunc()
		rf.changeState(Follower)
		// 注意还要改变votedFor=-1!!!这是一个大坑！！！
		// 表明已经知晓现在的任期号增加了，但是谁是该任期的leader还需要等待投票决定
		rf.currentTerm, rf.votedFor = reply.Term, -1
		rf.persist()
		return
	}
	rf.nextIndex[peer] = rf.log.firstEntry().Index + 1
	rf.matchIndex[peer] = rf.log.firstEntry().Index + 1
	DPrintf("(peer=%v, term=%v)snapshot installed, then send appendEntries", peer, reply.Term)
	go rf.sendAppendEntries(peer, ctx, cancelFunc)
}

// The ticker go routine starts a new election if this peer hasn't received
// heartbeats recently.
func (rf *Raft) ticker() {
	for rf.killed() == false {
		// Your code here to check if a leader election should
		// be started and to randomize sleeping time using
		// time.Sleep().
		//这里简化了一下实现，启动了一个rf之后两个timer会一直都存在，不管是什么state，
		//可以避免如：选举成为了leader后broadcast一次heartbeat，但因为heartbeatTimer早就结束了，
		//于是只会发送一次broadcast heartbeat，导致其他的rf只会收到一次heartbeat，不可避免地会超时进行选举
		//上边描述的问题具体可以看这个：https://github.com/OneSizeFitsQuorum/MIT6.824-2021/issues/15
		//这也是我参考的代码仓库，有另一种实现，也就是将两个timer的切换逻辑写在了ChangeState里边，似乎这种逻辑要更加清晰一些
		select {
		case <-rf.electionTimer.C:
			//参考figure 4，Candidate和Follower状态下都有机会切换成candidate开启一轮选举，唯独Leader不行
			if Leader != rf.state {
				go rf.startElection() // startElection 的时候貌似不能放在Lock里边，否则便只有成为leader或者竞选失败了才能被别的携程访问
			}
		case <-rf.heartbeatTimer.C:
			if Leader == rf.state {
				go rf.broadcastHeartbeat()
			}
		}
	}
}

func (rf *Raft) applier() {
	for !rf.killed() {
		<-rf.notifyApplyCh
		rf.mu.RLock()
		if rf.lastApplied >= rf.commitIndex {
			rf.mu.Unlock()
			continue
		}
		commitIndex, lastApplied := rf.commitIndex, rf.lastApplied
		firstIndex := rf.log.firstEntry().Index
		// 这个长度有点坑，区间应该是[lastApplied+1, commitIndex]，于是len应该是(lastApplied+1-commitIndex)+1，两个1抵消
		entries := make([]Entry, commitIndex-lastApplied)
		// 不论如何还是需要减去firstIndex，有可能因为覆盖等原因第一个entry不再是(0,0,nil)
		//因为没有减去firstIndex，让我在TestFailNoAgree2B上debug了很久
		copy(entries, rf.log.entries[lastApplied+1-firstIndex:commitIndex+1-firstIndex])
		rf.lastApplied = Max(rf.lastApplied, rf.commitIndex)
		rf.mu.RUnlock()

		for _, entry := range entries {
			rf.applyCh <- ApplyMsg{
				CommandValid: true,
				Command:      entry.Command,
				CommandIndex: entry.Index,
				CommandTerm:  entry.Term,
			}
		}
		DPrintf("【Apply】peer(me=%v,term=%v,state=%v) apply from %v to %v ,entries(%v)",
			rf.me, rf.currentTerm, rf.state, lastApplied+1, commitIndex, entries)
	}
}

// 需要持有锁
func (rf *Raft) changeState(state int) {
	if state == rf.state {
		return
	}
	if Follower == state {
		DPrintf("【Change State】peer:%v state %v➡%v", rf.me, rf.state, state)
		rf.heartbeatTimer.Stop()
		rf.electionTimer.Reset(ElectionTimeOut())
	} else if Leader == state {
		rf.electionTimer.Stop()
		rf.heartbeatTimer.Reset(HeartbeatTimeOut())
	}
	rf.state = state
}

func (rf *Raft) newAppendEntriesArgs(peer int) (*AppendEntriesArgs, int) {
	lastIndex, firstIndex := rf.log.lastEntry().Index, rf.log.firstEntry().Index
	nextIndex := rf.nextIndex[peer]
	preLogIndex := nextIndex - 1
	if preLogIndex < firstIndex {
		_ = fmt.Sprintf("【temp error】preLogIndex=%v, firstIndex=%v", preLogIndex, firstIndex)
	}
	preLogTerm := rf.log.entries[preLogIndex-firstIndex].Term // 特别强调一下取entry时候的索引，需要减去firstIndex

	args := &AppendEntriesArgs{
		Term:         rf.currentTerm,
		LeaderId:     rf.me,
		Entries:      []Entry{},
		PreLogIndex:  preLogIndex,
		PreLogTerm:   preLogTerm,
		LeaderCommit: rf.commitIndex,
	}

	if lastIndex >= nextIndex {
		args.Entries = rf.log.entries[nextIndex-firstIndex:]
	}
	//DPrintf("【BroadcastHeartbeat】Leader:%v term:%v to peer=%v. args(firstIndex=%v,lastIndex=%v,preLogIndex=%v,preLogTerm=%v,leaderCommit=%v)",
	//	rf.me, rf.currentTerm, peer, firstIndex, lastIndex, preLogIndex, preLogTerm, rf.commitIndex)
	return args, lastIndex
}

func (rf *Raft) newRequestVoteArgs() *RequestVoteArgs {
	lastEntry := rf.log.lastEntry()
	args := &RequestVoteArgs{
		Term:         rf.currentTerm,
		CandidateId:  rf.me,
		LastLogIndex: lastEntry.Index,
		LastLogTerm:  lastEntry.Term,
	}
	return args
}

func (rf *Raft) updateCommitIndex() {
	// 实现5.4.2 : raft never commit log entries from previous term by counting replicates.
	N := rf.log.lastEntry().Index
	firstIndex := rf.log.firstEntry().Index
	//如果存在 N 满足 N > commitIndex，matchIndex[i] ≥ N 对大部分 i 成立、log[N].term == currentTerm：设置 commitIndex = N
	for rf.log.entries[N-firstIndex].Term == rf.currentTerm { // 2.满足5.4.2约束
		count := 1
		for peer := range rf.peers {
			// 1.自己不会参与count计算，初始化的时候就默认算进去了(count := 1)
			// 3.更新
			if peer != rf.me && rf.matchIndex[peer] >= N {
				count++
			}
		}
		if count > len(rf.peers)/2 {
			rf.commitIndex = N
			go func() {
				rf.notifyApplyCh <- rf.commitIndex
			}()
			return
		}
		N--
	}
}

// Make
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int, persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{
		peers:          peers,
		persister:      persister,
		me:             me,
		dead:           0,
		electionTimer:  time.NewTimer(ElectionTimeOut()),
		heartbeatTimer: time.NewTimer(HeartbeatTimeOut()),
		applyCh:        applyCh,
		notifyApplyCh:  make(chan int),
		state:          Follower,
		//Persistent state on all servers
		currentTerm: 0,
		votedFor:    -1,
		log:         Log{},
		// Volatile state on all servers
		commitIndex: 0,
		lastApplied: 0,
		// Volatile state on leaders
		nextIndex:  make([]int, len(peers)),
		matchIndex: make([]int, len(peers)),
	}

	rf.heartbeatTimer.Stop() //初始状态不需要heartbeat，成为了leader才需要
	// Your initialization code here (2A, 2B, 2C).

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	// start ticker goroutine to start elections
	go rf.ticker()
	go rf.applier()

	defer DPrintf("【MAKE】 peer:%v term:%v,votedFor=%v,entries=%v", rf.me, rf.currentTerm, rf.votedFor, rf.log.entries)
	return rf
}
