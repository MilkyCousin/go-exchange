package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"os"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
)

// // global variables

// related to emails

var mailFrom = MailConfig{
	Address:  os.Getenv("EMAIL_ADDRESS"),
	Password: os.Getenv("EMAIL_PASSWORD"),
	SmtpHost: os.Getenv("EMAIL_SMTPHOST"),
	SmtpPort: os.Getenv("EMAIL_SMTPPORT"),
}

// related to db init

var dbData = DbConfig{
	Usr: "root",
	Pwd: os.Getenv("DATABASE_ROOT_PASSWORD"),
}

var db *sql.DB

// related to api operations

var apiToken string = os.Getenv("EXCHANGE_API_TOKEN") //"7e026dbcc375bd40cd0edb6d"

var url string = fmt.Sprintf("https://v6.exchangerate-api.com/v6/%s/pair/USD/UAH", apiToken)

// related to post operations

var postResults = []PostResult{
	{
		ResultCode:    http.StatusBadRequest,
		ResultMessage: "Неправильне значення аргумента",
	},
	{
		ResultCode:    http.StatusConflict,
		ResultMessage: "Поштову адресу вже додано",
	},
	{
		ResultCode:    http.StatusOK,
		ResultMessage: "Поштову адресу додано",
	},
}

// // structures

type MailConfig struct {
	Address  string
	Password string
	SmtpHost string
	SmtpPort string
}

type DbConfig struct {
	Usr string
	Pwd string
}

type PostResult struct {
	ResultCode    int16
	ResultMessage string
}

type CurrencyRate struct {
	Result             string  `json:"result"`
	Documentation      string  `json:"documentation"`
	TermsOfUse         string  `json:"terms_of_use"`
	TimeLastUpdateUnix int64   `json:"time_last_update_unix"`
	TimeLastUpdateUtx  string  `json:"time_last_update_utc"`
	TimeNextUpdateUnix int64   `json:"time_next_update_unix"`
	TimeNextUpdateUtx  string  `json:"time_next_update_utc"`
	BaseCode           string  `json:"base_code"`
	TargetCode         string  `json:"target_code"`
	ConversionRate     float64 `json:"conversion_rate"`
}

type EmailData struct {
	Email string `json:"email"`
}

type Form struct {
	BindingData string `form:"email" binding:"required"`
}

// // functions

// related to db

func connectToDb() {
	// setup db connection
	cfg := mysql.Config{
		User:   dbData.Usr,
		Passwd: dbData.Pwd,
		Net:    "tcp",
		Addr:   "database:3306",
		DBName: "DBEMAILS",
	}

	// try to connect to db using the credentials above
	var err error
	db, err = sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		log.Fatal(err)
	}

	pingErr := db.Ping()
	if pingErr != nil {
		log.Fatal(pingErr)
	}

	// check whether table in db is accessible
	_, err = db.Query("SELECT EMAIL FROM EMAILS;")
	if err != nil {
		log.Fatal(err)
	}
}

func addToDb(emailGiven EmailData) bool {
	var success bool = false

	var existsQuery string = `SELECT EXISTS (
		SELECT 1 FROM EMAILS
		WHERE EMAIL = ?
	);`
	var insertQuery string = `INSERT INTO EMAILS (EMAIL, DATEAT) VALUES (?, ?);`

	// check whether email is already in table
	var exists bool
	row := db.QueryRow(
		existsQuery,
		emailGiven.Email)

	// if not, try to insert into the table
	err := row.Scan(&exists)
	if (err == nil) && !exists {
		_, err := db.Exec(
			insertQuery,
			emailGiven.Email,
			time.Now())
		success = success || err == nil
	}

	return success
}

func getFromDb() ([]EmailData, bool) {
	var success bool = false
	var extracted []EmailData

	// retreive emails from table
	emails, err := db.Query("SELECT EMAIL FROM EMAILS;")

	if err == nil {
		defer emails.Close()

		for emails.Next() {
			var email EmailData
			if err := emails.Scan(&email.Email); err == nil {
				extracted = append(extracted, email)
				success = true
			}
		}
	}

	return extracted, success
}

// related to exchange rates api

func makeRequest() (CurrencyRate, bool) {
	var success bool = false
	var result CurrencyRate

	// get-request to exchange api
	resp, err := http.Get(url)
	if err == nil && resp.StatusCode == 200 {
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		var cond = (err == nil) && (json.Unmarshal(body, &result) == nil)
		success = success || cond
	}

	return result, success
}

// other

func formMessage(data CurrencyRate) string {
	// preparation step for mail sender
	var out string = fmt.Sprintf(
		"Вітаю. Поточний курс USD до UAH: %f",
		data.ConversionRate)
	return out
}

// related to web-interface

func getCurrency(ctx *gin.Context) {
	// retreive the currency rate from response
	outputBody, valid := makeRequest()

	var outputCode int
	if valid {
		outputCode = http.StatusOK
	} else {
		outputCode = http.StatusBadRequest
	}

	ctx.IndentedJSON(outputCode, struct {
		Number float64 `json:"number"`
	}{outputBody.ConversionRate})
}

func addEmail(ctx *gin.Context) {
	var emailPattern string = `^[\w-\.]+@([\w-]+\.)+[\w-]{2,4}$`
	var emailGiven EmailData
	var form Form

	// parameter value extraction and validation
	ctx.Bind(&form)
	emailGiven.Email = form.BindingData
	var emailLike, _ = regexp.MatchString(emailPattern, emailGiven.Email)

	// check whether email is valid and is new for db
	var successCnt int8 = 0
	if emailLike && len(emailGiven.Email) > 0 {
		successCnt += 1
		if success := addToDb(emailGiven); success {
			successCnt += 1
		}
	}

	// response to user
	var selectedResult = postResults[successCnt]
	ctx.IndentedJSON(int(selectedResult.ResultCode), struct {
		Details string `json:"details"`
	}{Details: selectedResult.ResultMessage})
}

func sendMail(ctx *gin.Context) {
	var success bool = true

	// get emails from db
	emails, successGet := getFromDb()

	if successGet && (len(emails) > 0) {
		// recall current rate
		data, _ := makeRequest()
		msgContent := []byte(formMessage(data))

		// iterate over emails and send the message
		for _, ej := range emails {
			var successLocal = false

			// authentification
			auth := smtp.PlainAuth("", mailFrom.Address, mailFrom.Password, mailFrom.SmtpHost)

			// send message
			to := []string{ej.Email}
			err := smtp.SendMail(mailFrom.SmtpHost+":"+mailFrom.SmtpPort, auth, mailFrom.Address, to, msgContent)
			if err != nil {
				log.Fatal(err)
			} else {
				successLocal = true
			}

			success = success && successLocal
		}
	} else {
		success = false
	}

	var outputCode int
	if success {
		outputCode = http.StatusOK
	} else {
		outputCode = http.StatusBadRequest
	}

	ctx.IndentedJSON(outputCode, struct{}{})
}

// general

func main() {
	// connect to db
	connectToDb()

	// create webserver instance
	router := gin.Default()

	// GET operations
	router.GET("/rate", getCurrency)

	// POST operations
	router.POST("/subscribe", addEmail)
	router.POST("/sendEmails", sendMail)

	// init webserver
	router.Run(fmt.Sprintf("0.0.0.0:%s", os.Getenv("WEB_PORT_PUBLISH")))
}
