package main

import (
	"time"

	"github.com/jinzhu/gorm"
)

func getConnection(uid string) *Connection {
	var connection Connection
	orm.DB.First(&connection, "client_id = ?", uid)

	return &connection
}

func getConnections() []*Connection {
	var connection []*Connection
	orm.DB.Find(&connection)

	return connection
}

func getConnectionByURL(urlCrm string) *Connection {
	var connection Connection
	orm.DB.First(&connection, "api_url = ?", urlCrm)

	return &connection
}

func (c *Connection) setConnectionActivity() error {
	return orm.DB.Model(c).Where("client_id = ?", c.ClientID).Update("Active", c.Active).Error
}

func (c *Connection) createConnection() error {
	return orm.DB.Create(c).Error
}

func (c *Connection) saveConnection() error {
	return orm.DB.Save(c).Error
}

func (c *Connection) saveConnectionByClientID() error {
	return orm.DB.Model(c).Where("client_id = ?", c.ClientID).Update(c).Error
}

func (c *Connection) createBot(b Bot) error {
	return orm.DB.Model(c).Association("Bots").Append(&b).Error
}

func getBotByToken(token string) (*Bot, error) {
	var bot Bot
	err := orm.DB.First(&bot, "token = ?", token).Error
	if gorm.IsRecordNotFoundError(err) {
		return &bot, nil
	} else {
		return &bot, err
	}

	return &bot, nil
}

func (b *Bot) save() error {
	return orm.DB.Save(b).Error
}

func (b *Bot) deleteBot() error {
	return orm.DB.Delete(b, "token = ?", b.Token).Error
}

func getBotChannelByToken(token string) uint64 {
	var b Bot
	orm.DB.First(&b, "token = ?", token)

	return b.Channel
}

func (c Connection) getBotsByClientID() Bots {
	var b Bots
	err := orm.DB.Model(c).Association("Bots").Find(&b).Error
	if err != nil {
		logger.Error(err)
	}

	return b
}

func getBot(cid int, ch uint64) *Bot {
	var bot Bot
	orm.DB.First(&bot, "connection_id = ? AND channel = ?", cid, ch)

	return &bot
}

func getConnectionById(id int) *Connection {
	var connection Connection
	orm.DB.First(&connection, "id = ?", id)

	return &connection
}

func (u *User) save() error {
	return orm.DB.Save(u).Error
}

func getUserByExternalID(eid int) *User {
	var user User
	orm.DB.First(&user, "external_id = ?", eid)

	return &user
}

//Expired method
func (u *User) Expired(updateInterval int) bool {
	return time.Now().After(u.UpdatedAt.Add(time.Hour * time.Duration(updateInterval)))
}
