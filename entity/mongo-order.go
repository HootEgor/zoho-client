package entity

import "time"

type MongoOrder struct {
	CreationDate time.Time `json:"creation_date" bson:"creation_date"`
	OrderID      int64     `json:"order_id" bson:"order_id"`
	Versions     []Version `json:"versions" bson:"versions"`
}

type Version struct {
	ID           string    `json:"id" bson:"id"`
	CreationDate time.Time `json:"creation_date" bson:"creation_date"`
	Payload      string    `json:"payload" bson:"payload"`
}
