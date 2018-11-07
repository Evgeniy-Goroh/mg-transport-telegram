package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/retailcrm/api-client-go/v5"
	"github.com/retailcrm/mg-transport-api-client-go/v1"
)

func connectHandler(c *gin.Context) {
	res := struct {
		Conn   Connection
		Locale map[string]interface{}
		Year   int
	}{
		c.MustGet("account").(Connection),
		getLocale(),
		time.Now().Year(),
	}

	c.HTML(http.StatusOK, "home", &res)
}

func addBotHandler(c *gin.Context) {
	b := c.MustGet("bot").(Bot)
	cl, err := getBotByToken(b.Token)
	if err != nil {
		c.Error(err)
		return
	}

	if cl.ID != 0 {
		c.AbortWithStatusJSON(BadRequest("bot_already_created"))
		return
	}

	bot, err := tgbotapi.NewBotAPI(b.Token)
	if err != nil {
		c.AbortWithStatusJSON(BadRequest("incorrect_token"))
		logger.Error(b.Token, err.Error())
		return
	}

	bot.Debug = config.Debug

	wr, err := bot.SetWebhook(tgbotapi.NewWebhook("https://" + config.HTTPServer.Host + "/telegram/" + bot.Token))
	if err != nil || !wr.Ok {
		c.AbortWithStatusJSON(BadRequest("error_creating_webhook"))
		logger.Error(b.Token, err.Error(), wr)
		return
	}

	b.Name = bot.Self.UserName
	conn := getConnectionById(b.ConnectionID)
	client := v1.New(conn.MGURL, conn.MGToken)
	client.Debug = config.Debug

	channelSettings := getChannelSettings()
	if b.Name != "" {
		channelSettings.Name = "@" + b.Name
	}

	data, status, err := client.ActivateTransportChannel(channelSettings)
	if status != http.StatusCreated {
		c.AbortWithStatusJSON(BadRequest("error_activating_channel"))
		logger.Error(conn.APIURL, status, err.Error(), data)
		return
	}

	b.Channel = data.ChannelID

	err = conn.createBot(b)
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusCreated, b)
}

func deleteBotHandler(c *gin.Context) {
	b := c.MustGet("bot").(Bot)
	conn := getConnectionById(b.ConnectionID)
	if conn.MGURL == "" || conn.MGToken == "" {
		c.AbortWithStatusJSON(BadRequest("not_found_account"))
		return
	}

	var client = v1.New(conn.MGURL, conn.MGToken)
	client.Debug = config.Debug

	data, status, err := client.DeactivateTransportChannel(getBotChannelByToken(b.Token))
	if status > http.StatusOK {
		c.AbortWithStatusJSON(BadRequest("error_deactivating_channel"))
		logger.Error(b.ID, status, err.Error(), data)
		return
	}

	err = b.deleteBot()
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

func settingsHandler(c *gin.Context) {
	uid := c.Param("uid")

	p := getConnection(uid)
	if p.ID == 0 {
		c.Redirect(http.StatusFound, "/")
		return
	}

	bots := p.getBotsByClientID()

	res := struct {
		Conn     *Connection
		Bots     Bots
		Locale   map[string]interface{}
		Year     int
		LangCode []string
	}{
		p,
		bots,
		getLocale(),
		time.Now().Year(),
		[]string{"en", "ru", "es"},
	}

	c.HTML(http.StatusOK, "form", &res)
}

func saveHandler(c *gin.Context) {
	conn := c.MustGet("connection").(Connection)
	_, err, code := getAPIClient(conn.APIURL, conn.APIKEY)
	if err != nil {
		if code == http.StatusInternalServerError {
			c.Error(err)
		} else {
			c.AbortWithStatusJSON(code, gin.H{"error": err.Error()})
		}
		return
	}

	err = conn.saveConnectionByClientID()
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": getLocalizedMessage("successful")})
}

func createHandler(c *gin.Context) {
	conn := c.MustGet("connection").(Connection)

	cl := getConnectionByURL(conn.APIURL)
	if cl.ID != 0 {
		c.AbortWithStatusJSON(BadRequest("connection_already_created"))
		return
	}

	client, err, code := getAPIClient(conn.APIURL, conn.APIKEY)
	if err != nil {
		if code == http.StatusInternalServerError {
			c.Error(err)
		} else {
			c.AbortWithStatusJSON(code, gin.H{"error": err.Error()})
		}
		return
	}

	conn.ClientID = GenerateToken()
	data, status, errr := client.IntegrationModuleEdit(getIntegrationModule(conn.ClientID))
	if errr.RuntimeErr != nil {
		c.Error(errr.RuntimeErr)
		return
	}

	if status == http.StatusPaymentRequired {
		c.AbortWithStatusJSON(BadRequest("error_payment_mg"))
		logger.Error(conn.APIURL, status, errr.ApiErr, data)
		return
	}

	if status >= http.StatusBadRequest {
		c.AbortWithStatusJSON(BadRequest("error_activity_mg"))
		logger.Error(conn.APIURL, status, errr.ApiErr, data)
		return
	}

	conn.MGURL = data.Info.MgTransportInfo.EndpointUrl
	conn.MGToken = data.Info.MgTransportInfo.Token
	conn.Active = true

	err = conn.createConnection()
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(
		http.StatusCreated,
		gin.H{
			"url":     "/settings/" + conn.ClientID,
			"message": getLocalizedMessage("successful"),
		},
	)
}

func activityHandler(c *gin.Context) {
	var (
		activity  v5.Activity
		systemUrl = c.PostForm("systemUrl")
		clientId  = c.PostForm("clientId")
	)

	conn := getConnection(clientId)
	if conn.ID == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			gin.H{
				"success": false,
				"error":   "Wrong data",
			},
		)
		return
	}

	err := json.Unmarshal([]byte(c.PostForm("activity")), &activity)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			gin.H{
				"success": false,
				"error":   "Wrong data",
			},
		)
		return
	}

	conn.Active = activity.Active && !activity.Freeze

	if systemUrl != "" {
		conn.APIURL = systemUrl
	}

	if err := conn.saveConnection(); err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func setLangBotHandler(c *gin.Context) {
	b := c.MustGet("bot").(Bot)
	cl, err := getBotByToken(b.Token)
	if err != nil {
		c.Error(err)
		return
	}

	cl.Lang = b.Lang

	err = cl.save()
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

func getIntegrationModule(clientId string) v5.IntegrationModule {
	return v5.IntegrationModule{
		Code:            config.TransportInfo.Code,
		IntegrationCode: config.TransportInfo.Code,
		Active:          true,
		Name:            config.TransportInfo.Name,
		ClientID:        clientId,
		Logo: fmt.Sprintf(
			"https://%s%s",
			config.HTTPServer.Host,
			config.TransportInfo.LogoPath,
		),
		BaseURL: fmt.Sprintf(
			"https://%s",
			config.HTTPServer.Host,
		),
		AccountURL: fmt.Sprintf(
			"https://%s/settings/%s",
			config.HTTPServer.Host,
			clientId,
		),
		Actions: map[string]string{"activity": "/actions/activity"},
		Integrations: &v5.Integrations{
			MgTransport: &v5.MgTransport{
				WebhookUrl: fmt.Sprintf(
					"https://%s/webhook/",
					config.HTTPServer.Host,
				),
			},
		},
	}
}

func getChannelSettings(cid ...uint64) v1.Channel {
	var channelID uint64

	if len(cid) > 0 {
		channelID = cid[0]
	}

	return v1.Channel{
		ID:   channelID,
		Type: Type,
		Settings: v1.ChannelSettings{
			SpamAllowed: false,
			Status: v1.Status{
				Delivered: v1.ChannelFeatureSend,
				Read:      v1.ChannelFeatureNone,
			},
			Text: v1.ChannelSettingsText{
				Creating: v1.ChannelFeatureBoth,
				Editing:  v1.ChannelFeatureBoth,
				Quoting:  v1.ChannelFeatureBoth,
				Deleting: v1.ChannelFeatureReceive,
			},
			Product: v1.Product{
				Creating: v1.ChannelFeatureReceive,
				Editing:  v1.ChannelFeatureReceive,
			},
			Order: v1.Order{
				Creating: v1.ChannelFeatureReceive,
				Editing:  v1.ChannelFeatureReceive,
			},
		},
	}
}

func updateChannelsSettings() {
	hashSettings, err := getChannelSettingsHash()
	if err != nil {
		logger.Error(err.Error())
		return
	}

	connections := getConnections()
	if len(connections) > 0 {
		for _, conn := range connections {
			if !conn.Active {
				logger.Infof(
					"updateChannelsSettings connection %s deactivated",
					conn.APIURL,
				)
				continue
			}
			updateBots(conn, hashSettings)
		}
	}

	return
}

func updateBots(conn *Connection, hashSettings string) {
	bots := conn.getBotsByClientID()
	if len(bots) > 0 {
		client := v1.New(conn.MGURL, conn.MGToken)
		client.Debug = config.Debug
		for _, bot := range bots {
			if bot.ChannelSettingsHash == hashSettings {
				continue
			}

			channelSettings := getChannelSettings(bot.Channel)
			if bot.Name != "" {
				channelSettings.Name = "@" + bot.Name
			}

			data, status, err := client.UpdateTransportChannel(channelSettings)
			if config.Debug {
				logger.Infof(
					"updateChannelsSettings apiURL: %s, ChannelID: %d, Data: %v, Status: %d, err: %v",
					conn.APIURL, bot.Channel, data, status, err,
				)
			}

			if err == nil {
				bot.ChannelSettingsHash = hashSettings
				err = bot.save()
				if err != nil {
					logger.Error(
						"updateChannelsSettings bot.save apiURL: %s, bot.Channel: %d , err: %v",
						conn.APIURL, bot.Channel, err,
					)
				}
			}

		}
	}

	return
}

func telegramWebhookHandler(c *gin.Context) {
	b := c.MustGet("bot").(Bot)

	conn := getConnectionById(b.ConnectionID)
	if !conn.Active {
		c.AbortWithStatus(http.StatusOK)
		return
	}

	var update tgbotapi.Update
	if err := c.ShouldBindJSON(&update); err != nil {
		c.Error(err)
		return
	}

	if config.Debug {
		logger.Debugf(
			"mgWebhookHandler request:\nUpdateID: %v,\nMessage: %+v,\nEditedMessage: %+v",
			update.UpdateID, update.Message, update.EditedMessage,
		)
	}

	var client = v1.New(conn.MGURL, conn.MGToken)
	client.Debug = config.Debug

	if update.Message != nil {
		if update.Message.Text == "" {
			setLocale(update.Message.From.LanguageCode)
			update.Message.Text = getLocalizedMessage(getMessageID(update.Message))
		}

		nickname := update.Message.From.UserName
		user := getUserByExternalID(update.Message.From.ID)

		if update.Message.From.UserName == "" {
			nickname = update.Message.From.FirstName
		}

		if user.Expired(config.UpdateInterval) || user.ID == 0 {
			fileID, fileURL, err := GetFileIDAndURL(b.Token, update.Message.From.ID)
			if err != nil {
				c.Error(err)
				return
			}

			if fileID != user.UserPhotoID && fileURL != "" {
				picURL, err := UploadUserAvatar(fileURL)
				if err != nil {
					c.Error(err)
					return
				}

				user.UserPhotoID = fileID
				user.UserPhotoURL = picURL
			}

			if user.ExternalID == 0 {
				user.ExternalID = update.Message.From.ID
			}

			err = user.save()
			if err != nil {
				c.Error(err)
				return
			}
		}

		lang := update.Message.From.LanguageCode

		if len(update.Message.From.LanguageCode) > 2 {
			lang = update.Message.From.LanguageCode[:2]
		}

		if config.Debug {
			logger.Debugf("telegramWebhookHandler user %+v", user)
		}

		snd := v1.SendData{
			Message: v1.Message{
				ExternalID: strconv.Itoa(update.Message.MessageID),
				Type:       "text",
				Text:       update.Message.Text,
			},
			User: v1.User{
				ExternalID: strconv.Itoa(update.Message.From.ID),
				Nickname:   nickname,
				Firstname:  update.Message.From.FirstName,
				Avatar:     user.UserPhotoURL,
				Lastname:   update.Message.From.LastName,
				Language:   lang,
			},
			Channel:        b.Channel,
			ExternalChatID: strconv.FormatInt(update.Message.Chat.ID, 10),
		}

		if update.Message.ReplyToMessage != nil {
			snd.Quote = &v1.SendMessageRequestQuote{ExternalID: strconv.Itoa(update.Message.ReplyToMessage.MessageID)}
		}

		data, st, err := client.Messages(snd)
		if err != nil {
			logger.Error(b.Token, err.Error(), st, data)
			c.Error(err)
			return
		}

		if config.Debug {
			logger.Debugf("telegramWebhookHandler Type: SendMessage, Bot: %v, Message: %+v, Response: %+v", b.ID, snd, data)
		}
	}

	if update.EditedMessage != nil {
		if update.EditedMessage.Text == "" {
			setLocale(update.EditedMessage.From.LanguageCode)
			update.EditedMessage.Text = getLocalizedMessage(getMessageID(update.Message))
		}

		snd := v1.UpdateData{
			Message: v1.UpdateMessage{
				Message: v1.Message{
					ExternalID: strconv.Itoa(update.EditedMessage.MessageID),
					Type:       "text",
					Text:       update.EditedMessage.Text,
				},
			},
			Channel: b.Channel,
		}

		data, st, err := client.UpdateMessages(snd)
		if err != nil {
			logger.Error(b.Token, err.Error(), st, data)
			c.Error(err)
			return
		}

		if config.Debug {
			logger.Debugf("telegramWebhookHandler Type: UpdateMessage, Bot: %v, Message: %v, Response: %v", b.ID, snd, data)
		}
	}

	c.JSON(http.StatusOK, gin.H{})
}

func mgWebhookHandler(c *gin.Context) {
	conn := c.MustGet("connection").(Connection)

	var msg v1.WebhookRequest
	if err := c.ShouldBindJSON(&msg); err != nil {
		c.Error(err)
		return
	}

	if config.Debug {
		logger.Debugf("mgWebhookHandler request: %+v", msg)
	}

	uid, _ := strconv.Atoi(msg.Data.ExternalMessageID)
	cid, _ := strconv.ParseInt(msg.Data.ExternalChatID, 10, 64)

	b := getBot(conn.ID, msg.Data.ChannelID)
	if b.ID == 0 {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	bot, err := tgbotapi.NewBotAPI(b.Token)
	if err != nil {
		logger.Error(b, err)
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	bot.Debug = config.Debug
	setLocale(b.Lang)

	switch msg.Type {
	case "message_sent":
		var mb string
		switch msg.Data.Type {
		case v1.MsgTypeProduct:
			mb = fmt.Sprintf("*%s*\n", replaceMarkdownSymbols(msg.Data.Product.Name))

			if msg.Data.Product.Cost != nil && msg.Data.Product.Cost.Value != 0 {
				mb += fmt.Sprintf(
					"\n%s: %s\n",
					getLocalizedMessage("item_cost"),
					getLocalizedTemplateMessage(
						"cost_currency",
						map[string]interface{}{
							"Amount":   msg.Data.Product.Cost.Value,
							"Currency": currency[strings.ToLower(msg.Data.Product.Cost.Currency)],
						},
					),
				)
			}

			if msg.Data.Product.Url != "" {
				mb += replaceMarkdownSymbols(msg.Data.Product.Url)
			} else {
				mb += replaceMarkdownSymbols(msg.Data.Product.Img)
			}
		case v1.MsgTypeOrder:
			mb = getOrderMessage(msg.Data.Order)
		case v1.MsgTypeText:
			mb = msg.Data.Content
		}

		m := tgbotapi.NewMessage(cid, mb)
		if msg.Data.QuoteExternalID != "" {
			qid, err := strconv.Atoi(msg.Data.QuoteExternalID)
			if err != nil {
				c.Error(err)
				return
			}
			m.ReplyToMessageID = qid
		}

		m.ParseMode = "Markdown"

		msgSend, err := bot.Send(m)
		if err != nil {
			logger.Error(err)
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}

		if config.Debug {
			logger.Debugf("mgWebhookHandler sent %+v", msgSend)
		}

		c.JSON(http.StatusOK, gin.H{"external_message_id": strconv.Itoa(msgSend.MessageID)})

	case "message_updated":
		msgSend, err := bot.Send(tgbotapi.NewEditMessageText(cid, uid, msg.Data.Content))
		if err != nil {
			logger.Error(err)
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}

		if config.Debug {
			logger.Debugf("mgWebhookHandler update %+v", msgSend)
		}

		c.AbortWithStatus(http.StatusOK)

	case "message_deleted":
		msgSend, err := bot.Send(tgbotapi.NewDeleteMessage(cid, uid))
		if err != nil {
			logger.Error(err)
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}

		if config.Debug {
			logger.Debugf("mgWebhookHandler delete %+v", msgSend)
		}

		c.JSON(http.StatusOK, gin.H{})

	}
}

func getOrderMessage(dataOrder *v1.MessageDataOrder) string {
	mb := "*" + getLocalizedMessage("order")

	if dataOrder.Number != "" {
		mb += " " + replaceMarkdownSymbols(dataOrder.Number)
	}

	if dataOrder.Date != "" {
		mb += fmt.Sprintf(" (%s)", dataOrder.Date)
	}
	mb += "*\n"
	if len(dataOrder.Items) > 0 {
		mb += "\n"
		for k, v := range dataOrder.Items {
			mb += fmt.Sprintf(
				"%d. %s",
				k+1,
				replaceMarkdownSymbols(v.Name),
			)

			if v.Quantity != nil {
				if v.Quantity.Value != 0 {
					mb += fmt.Sprintf(
						" _%v_",
						v.Quantity.Value,
					)
				}
			}

			if v.Price != nil {
				if val, ok := currency[strings.ToLower(v.Price.Currency)]; ok {
					mb += fmt.Sprintf(
						" _x %s_\n",
						getLocalizedTemplateMessage(
							"cost_currency",
							map[string]interface{}{
								"Amount":   v.Price.Value,
								"Currency": val,
							},
						),
					)
				}
			} else {
				mb += "\n"
			}
		}
	}

	if dataOrder.Delivery != nil {
		if dataOrder.Delivery.Name != "" {
			mb += fmt.Sprintf(
				"\n*%s:*\n%s",
				getLocalizedMessage("delivery"),
				replaceMarkdownSymbols(dataOrder.Delivery.Name),
			)
		}

		if dataOrder.Delivery.Price != nil {
			if val, ok := currency[strings.ToLower(dataOrder.Delivery.Price.Currency)]; ok && dataOrder.Delivery.Price.Value != 0 {
				mb += fmt.Sprintf(
					"; %s",
					getLocalizedTemplateMessage(
						"cost_currency",
						map[string]interface{}{
							"Amount":   dataOrder.Delivery.Price.Value,
							"Currency": val,
						},
					),
				)
			}
		}

		if dataOrder.Delivery.Address != "" {
			mb += ";\n" + replaceMarkdownSymbols(dataOrder.Delivery.Address)
		}

		mb += "\n"
	}

	if len(dataOrder.Payments) > 0 {
		mb += fmt.Sprintf(
			"\n*%s:*\n",
			getLocalizedMessage("payment"),
		)
		for _, v := range dataOrder.Payments {
			mb += replaceMarkdownSymbols(v.Name)

			if v.Amount != nil {
				if val, ok := currency[strings.ToLower(v.Amount.Currency)]; ok && v.Amount.Value != 0 {
					mb += fmt.Sprintf(
						"; %s",
						getLocalizedTemplateMessage(
							"cost_currency",
							map[string]interface{}{
								"Amount":   v.Amount.Value,
								"Currency": val,
							},
						),
					)
				}
			}

			if v.Status != nil && v.Status.Name != "" {
				mb += fmt.Sprintf(
					" (%s)",
					replaceMarkdownSymbols(v.Status.Name),
				)
			}

			mb += "\n"
		}
	}

	if dataOrder.Cost != nil {
		if val, ok := currency[strings.ToLower(dataOrder.Cost.Currency)]; ok && dataOrder.Cost.Value != 0 {
			mb += fmt.Sprintf(
				"\n%s: %s",
				getLocalizedMessage("order_total"),
				getLocalizedTemplateMessage(
					"cost_currency",
					map[string]interface{}{
						"Amount":   dataOrder.Cost.Value,
						"Currency": val,
					},
				),
			)
		}
	}

	return mb
}
