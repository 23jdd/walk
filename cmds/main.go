package main

import (
	"fmt"

	"github.com/23jdd/walk"
)

func insertNodes(db *walk.DB) {
	games := []string{
		"Minecraft",
		"Grand Theft Auto V",
		"Red Dead Redemption 2",
		"The Witcher 3: Wild Hunt",
		"Elden Ring",
		"Cyberpunk 2077",
		"The Legend of Zelda: Breath of the Wild",
		"Black Myth: Wukong",
		"Terraria",
		"Stardew Valley",
	}
	for id, game := range games {
		err := db.InsertNodeWithId(walk.NewNodesWithId(uint64(id)).WithLabels("Game").WithProperties(map[string]walk.Value{
			"name": walk.ValueString(game),
		}))
		if err != nil {
			panic(err)
		}
	}
}
func main() {
	db, err := walk.Open("walk.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	insertNodes(db)
	for id := range db.GetNodeByLabel("Game") {
		game, err := db.GetNode(id)
		if err != nil {
			panic(err)
		}
		fmt.Println(game.Labels)
		for k, v := range game.Properties {
			fmt.Println(k, ": ", string(v.Content))
		}
	}
}
