package main

import "testing"

func TestUnit_IT033_CounterpartyResponseQueueIsRegistered(t *testing.T) {
	for _, definition := range workerQueueDefinitions {
		if definition[0] == "counterparty-responded" && definition[1] == "counterparty.responded.v1" {
			return
		}
	}
	t.Fatal("IT-033 counterparty response event has no durable worker queue")
}
