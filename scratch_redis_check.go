package main

import (
	"fmt"
	"github.com/redis/go-redis/v9"
)

func main() {
	client := redis.NewFailoverClient(&redis.FailoverOptions{
		MasterName:    "mymaster",
		SentinelAddrs: []string{"localhost:26379"},
	})
	
	fmt.Printf("Type: %T\n", client)
}
