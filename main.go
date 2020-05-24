package main

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gufeijun/baiduwenku/config"
	"github.com/gufeijun/baiduwenku/filetype"
	"github.com/gufeijun/baiduwenku/mediumware"
	"github.com/gufeijun/baiduwenku/model"
	"github.com/gufeijun/baiduwenku/utils"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var a map[string]time.Time = make(map[string]time.Time)

func main(){
	//启用定时器，每个下载的文件服务器暂存2小时
	go Timer1()
	//定时器，每天定时12点更新用户剩余的下载次数
	go Timer2()

	router := gin.Default()
	router.Static("/static", "front-end")
	router.LoadHTMLGlob("front-end/html/*.html")

	//主页面
	router.GET("/baiduspider", func(c *gin.Context) {
		cookie, _ := c.Request.Cookie("sessionid")
		var emailadd string
		if cookie!=nil{
			sessionid := cookie.Value
			query := "select emailadd from hustsessions where sessionid=?"
			row := config.Db.QueryRow(query, sessionid)
			row.Scan(&emailadd)
			emailadd=strings.Split(emailadd,"@")[0]
		}
		remain,_:=utils.GetDownloadTicket()
		c.HTML(http.StatusOK,"home.html",struct {
			Emailadd string
			Remain int
		}{emailadd,remain})
	})

	//文件下载api
	router.POST("/baiduspider",func(c *gin.Context){
		url,ok:=c.GetPostForm("url")
		if !ok{
			c.JSON(http.StatusOK,gin.H{
				"status":0,
				"err":"Can Not Parse URL!",
			})
			return
		}
		var filepath string
		var err error

		//根据不同登录状态启用不同的函数
		if !model.CheckSession(c){  //未登录
			filepath,err=spider(url)
			a[filepath]=time.Now()
			filepath="/download/?file="+filepath
		}else{
			user,err1:=model.GetUserInfo(c)
			if err1!=nil{
				c.JSON(http.StatusOK,gin.H{
					"status":0,
					"err":err1.Error(),
				})
				return
			}
			filepath,err=advancedDownload(url,user)
		}
		if err!=nil{
			c.JSON(http.StatusOK,gin.H{
				"status":0,
				"err":err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK,gin.H{
			"status":1,
			"path":filepath,
		})
	})

	//文件下载
	router.GET("/download", func(c *gin.Context) {
		name,ok:=c.GetQuery("file")
		if !ok{
			c.String(http.StatusBadRequest,"illegal!")
			return
		}
		//判断文件是否存在
		fileinfo,err:=os.Stat(name)
		if err!=nil{
			c.String(http.StatusBadRequest,"No Such File!")
			return
		}
		filesize:=strconv.FormatInt(fileinfo.Size(),10)
		//限制文件大小在50M内
		if fileinfo.Size()>50<<20{
			c.String(http.StatusForbidden,"Too large file!")
			return
		}
		//防止下载服务器配置文件
		if strings.Contains(name,"config.json"){
			return
		}
		c.Writer.WriteHeader(http.StatusOK)
		c.Header("Content-Disposition", "attachment; filename="+name)
		c.Header("Content-Type", "application/octet-stream")
		c.Header("Content-Length",filesize)
		c.File(name)
	})

	//用户注册页面
	router.GET("/hustregister",func(c *gin.Context){
		c.HTML(http.StatusOK,"regist.html",nil)
	})

	//向用户邮箱发送验证码
	router.POST("/hustregister/code", mediumware.LimitTimeMediumware(),func(c *gin.Context) {
		c.JSON(http.StatusOK,gin.H{
			"status":1,
		})
		var user *model.User
		c.ShouldBind(&user)
		//生成一个六位随机数字的验证码
		var code string
		rand.Seed(time.Now().Unix())
		for i := 0; i < 6; i++ {
			code += strconv.Itoa(rand.Intn(10))
		}
		config.VerificationCode[user.EmailAdd] = config.M{Code: code,Time: time.Now()}
		//向用户发送验证码
		utils.SendCode(user.EmailAdd, code)
	})

	//注册api
	router.POST("/hustregister",mediumware.FormatCheck,func(c *gin.Context){
		var user *model.User
		if err := c.ShouldBind(&user); err != nil {
			c.JSON(200, gin.H{
				"status": 0,
				"err":    "表单解析错误！",
			})
			return
		}
		//查看是否是华科邮箱，进而赋予不同的权限
		emailTail:=strings.Split(user.EmailAdd,"@")[1]
		//普通人权限为0，huster为1
		if emailTail=="hust.edu.cn"{
			user.PermissionCode=config.HUSTER_CODE
		}
		if err:=user.AddUser();err!=nil{
			c.JSON(http.StatusBadRequest,gin.H{
				"status":0,
				"err":err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status": 1,
		})
		delete(config.VerificationCode, user.EmailAdd)
	})

	//登录api
	router.POST("/husterlogin", func(c *gin.Context) {
		var user *model.User
		if err:=c.ShouldBind(&user);err!=nil{
			c.JSON(http.StatusOK,gin.H{
				"status":0,
			})
			return
		}
		if p:=user.CheckLogin();p==config.WRONG_PASSWORD{
			c.JSON(http.StatusOK,gin.H{
				"status":0,
				"err":config.WRONG_PASSWORD,
			})
			return
		}else if p==config.NOT_REGISTERED{
			c.JSON(http.StatusOK,gin.H{
				"status":0,
				"err":config.NOT_REGISTERED,
			})
			return
		}
		sessionid:=model.NewSessionID(user.EmailAdd)
		c.SetCookie("sessionid", sessionid, 2592000, "/", config.SeverConfig.DOMAIN, false,true)
		c.JSON(200, gin.H{
			"status": 1,
		})
	})

	//登出
	router.GET("/logout", func(c *gin.Context) {
		c.SetCookie("sessionid", "nil", -1, "/", config.SeverConfig.DOMAIN,false, true)
		c.Redirect(http.StatusFound, "/baiduspider")
	})

	router.Run(config.SeverConfig.LISTEN_ADDRESS+":"+config.SeverConfig.LISTEN_PORT)
}

//未登录用户调用的爬虫函数
func spider(url string)(filepath string,err error) {
	//获取文档格式
	docType,err:=utils.GetDocType(url)
	if err!=nil{
		return "",errors.New("老夫暂时拿此链接无能为力~（´Д`）")
	}
	switch docType {
	case "txt":
		return filetype.StartTxtSpider(url)
	case "doc":
		return filetype.StartDocSpider(url)
	case "pdf":
		return filetype.StartPdfSpider(url)
	case "ppt":
		return filetype.StartPPTSpider(url)
	default:
		return "",errors.New(fmt.Sprintf("Do Not Support filetype:%s!",docType))
	}
	return
}

//登陆用户下载调用的函数
func advancedDownload(urls string,user *model.User)(filepath string,err error){
	//如果普通用户没有剩余下载次数
	if user.PermissionCode!=config.HUSTER_CODE&&user.Remain==0{
		return "",errors.New("今日的三次下载次数用完！")
	}
	infos,ifprofession,err:=utils.GetInfos(urls)
	if err!=nil{
		return
	}
	//如果当前下载文档为专享文档
	if ifprofession{
		remain,err:=utils.GetDownloadTicket()
		if err!=nil{
			return "",err
		}
		//权限不足
		if user.PermissionCode!=config.HUSTER_CODE{
			return "",errors.New("仅供华中大用户下载专享VIP文档!")
		}
		if remain==0{
			return "",errors.New("无剩余专享文档下载券！")
		}
	}
	client:=&http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse  //停止重定向，直接把下载连接发送给用户，节省服务器带宽
		},
	}
	val:=url.Values{
		//"ct": {"20008"},
		"doc_id": {infos[0]},
		//"retType": {"newResponse"},
		//"sns_type": {""},
		"storage": {"1"},
		//"useTicket": {"0"},
		//"target_uticket_num": {"0"},
		"downloadToken": {infos[2]},
		//"sz": {"37097"},
		//"v_code": {"0"},
		//"v_input": {"0"},
		"req_vip_free_doc": {"1"}, //共享文档应设为0
	}
	req,err:=http.NewRequest("POST","https://wenku.baidu.com/user/submit/download",strings.NewReader(val.Encode()))
	if err!=nil{
		return
	}
	req.Header.Add("User-Agent","Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/81.0.4044.138 Safari/537.36")
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	cookie:=&http.Cookie{
		Name: "BDUSS",
		Value: config.SeverConfig.BDUSS,
	}
	req.AddCookie(cookie)
	resp,err:=client.Do(req)
	if err!=nil{
		return
	}
	resp.Body.Close()
	location:=resp.Header.Get("Location")
	//如果获取重定向地址失败，尝试将"req_vip_free_doc"参数改为1重新请求
	if location==""{
		val.Set("req_vip_free_doc","0")
		//更改请求体
		req.Body=ioutil.NopCloser(strings.NewReader(val.Encode()))
		resp,err=client.Do(req)
		if err!=nil{
			return
		}
		resp.Body.Close()
		location=resp.Header.Get("Location")
		if location==""{
			return "",errors.New("无法下载该文件！")
		}
	}
	//普通用户下载完成后，今日剩余下载次数减一
	if user.PermissionCode!=config.HUSTER_CODE{
		user.UpdateUser()
	}
	return location,nil
}

//Timer1 定时器，爬虫下载的文件120分钟后删除后删除，精度不高，最大有60分钟偏差
func Timer1(){
	for{
		time.Sleep(60*time.Minute)
		for key,val:=range a{
			sub:=int(time.Since(val).Minutes())
			if sub>120{
				os.Remove(key)
			}
		}
	}
}

//Timer2 定时器，每天凌晨12点重置用户的剩余下载次数
func Timer2(){
	for {
		now := time.Now()
		next := now.Add(time.Hour * 24)
		next = time.Date(next.Year(), next.Month(), next.Day(), 0,0,0,0,next.Location())
		t := time.NewTimer(next.Sub(now))
		<-t.C
		if err:=model.UpdateAll();err!=nil{
			log.Println(err)
		}
	}
}