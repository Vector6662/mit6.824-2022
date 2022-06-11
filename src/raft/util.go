package raft

import (
	"fmt"
	"log"
	rand2 "math/rand"
	"time"
)

// Debugging
const Debug = false

func DPrintf(format string, a ...interface{}) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

func Format(format string, a ...interface{}) string {
	return fmt.Sprintf(format, a...)
}

// Min 一个有趣的现象，go里边没有整形的min/max方法：https://studygolang.com/articles/25200
func Min(x, y int) int {
	if x > y {
		return y
	}
	return x
}

func Max(x, y int) int {
	if x > y {
		return x
	}
	return y
}

// ElectionTimeOut randomize
func ElectionTimeOut() time.Duration {
	rand := time.Duration(800+rand2.Intn(150)) * time.Millisecond //[100,150)毫秒内的随机等待时间
	return rand
}

// HeartbeatTimeOut stable
func HeartbeatTimeOut() time.Duration {
	n := 125 * time.Millisecond
	return n
}
