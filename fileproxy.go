package main

import (
	"bytes"
	"flag"
	"github.com/dchest/uniuri"
	"github.com/hoisie/web"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"time"
)

var WAIT_CLIENT_TIMEOUT = 60 * time.Minute

type AppConfig struct {
	Scheme string
	Host   string

	BindScheme string
	BindHost   string

	UseXForwardedFor bool
}

var appConfig AppConfig

type uploadRequest struct {
	request *http.Request
	wait    chan string
}

var uploadRequests = make(map[string]uploadRequest)

type IndexTemplateValue struct {
	AppConfig
	Key string
}

var indexTemplate = `<html>
<head>
  <title>ファイル転送くん</title>
  <link href="/css/bootstrap.min.css" rel="stylesheet">
  <style>
    body {
        padding-top: 20px;
    }
  </style>
</head>
<body>
<div class="container text-center">
  <div class="hero-unit">
    <h1>ファイル転送くん</h1>
    <br />
    <ol>
      <li>ファイルを選んで「転送開始」を押す。</li>
      <li>出てきた URL を相手に教えて、開いてもらう。</li>
      <li>転送が始まるので、気長に待つ。</li>
    </ol>
  </div>

  <p id="download-url">この URL を相手に開いてもらってね。 <b>{{.Scheme}}://{{.Host}}/download/{{.Key}}<b></p>

  <form id="upload-form" action="./upload/{{.Key}}" method="POST" enctype="multipart/form-data">
  <input type="hidden" name="size" value="-1" />
  ファイル: <input type="file" name="file" />
  <input id="upload-btn" class="btn" type="submit" value="転送開始" />
  </form>

  <div id="progress-info" class="alert alert-info" style="display: none;">
    <p id="progress-msg">
      相手接続待ち…
    </p>
    <div id="progress-container" class="progress progress-striped active">
      <div id="progress-bar" class="bar" style="width: 0%"></div>
    </div>
  </div>
</div>
<script src="/js/jquery.min.js"></script>
<script src="/js/bootstrap.min.js"></script>
<script>
$(document).ready(function() {
    $('#download-url').hide();
    $('#upload-form').on('submit', function(event) {
        var form = event.target;

        if (!form.file.value) {
            return false;
        }

        if (form.file.files[0].size) {
            form.size.value = form.file.files[0].size;
        }

        // some broken browsers don't support FormData
        if (!FormData) {
            $('#download-url').show();
            return true;
        }

        var data = new FormData(form);

        var xhr = $.ajax({
            url: form.action,
            type: 'POST',
            xhr: function() {
                var req = $.ajaxSettings.xhr();
                if (req) {
                    req.upload.addEventListener('progress', function(event) {
                        // because some bytes may be bufferd on client/server,
                        // ignore < 2M bytes
                        console.log(event.loaded);
                        if (event.loaded > 2 * 1024 * 1024) {
                            $('#progress-msg').text('転送中…');
                        }

                        if (event.lengthComputable) {

                            var percentage = event.loaded / event.total * 100;
                            $('#progress-bar')[0].style.width = percentage + '%';
                        }
                    }, false);
                }

                return req;
            },
            beforeSend: function(xhr) {
                $('#download-url').show();
                $('#progress-info').show();
            },
            success: function(result, status) {
                $('#progress-msg').text('転送完了');
                $('#progress-bar')[0].style.width = '100%';
            },
            error: function(xhr, status) {
                $('#progress-msg').text('転送失敗');
            },
            complete: function() {
                $('#progress-container').removeClass('active');
            },
            data: data,
            cache: false,
            contentType: false,
            processData: false
        });

        return false;
    });
});
</script>
</body>
</html>
`

type UploadTemplateValue struct {
	Message string
}

var uploadTemplate = `<html>
<head>
  <title>ファイル転送くん</title>
  <link href="/css/bootstrap.min.css" rel="stylesheet">
  <style>
    body {
        padding-top: 20px;
    }
  </style>
</head>
<body>
<div class="container text-center">
  <div class="hero-unit">
    <h1>ファイル転送くん</h1>
    <br />
    <ol>
      <li>ファイルを選んで「転送開始」を押す。</li>
      <li>出てきた URL を相手に教えて、開いてもらう。</li>
      <li>転送が始まるので、気長に待つ。</li>
    </ol>
  </div>

  <p>{{.Message}}</p>

  <p><a href="/">戻る</a></p>

</body>
</html>
`

func indexPage(ctx *web.Context) {
	updateRemoteAddr(ctx)

	tmpl, err := template.New("index").Parse(indexTemplate)

	if err != nil {
		panic(err)
	}

	key := uniuri.New()
	v := IndexTemplateValue{appConfig, key}

	if ctx.Request.Host != "" {
		v.Host = ctx.Request.Host
	}
	if v.Scheme == "" {
		v.Scheme = appConfig.BindScheme
	}
	tmpl.Execute(ctx, v)
}

func uploader(ctx *web.Context, key string) {
	updateRemoteAddr(ctx)

	if _, ok := uploadRequests[key]; ok {
		// key exists
		ctx.Forbidden()
		return
	}

	wait := make(chan string)

	defer delete(uploadRequests, key)
	uploadRequests[key] = uploadRequest{ctx.Request, wait}

	var result string
	select {
	case result = <-wait:
	case <-time.After(WAIT_CLIENT_TIMEOUT):
		result = "wait client timeout"
	}

	if result == "connected" {
		// wait actual result
		result = <-wait
	}

	var body string

	if xrw := ctx.Request.Header.Get("X-Requested-With"); xrw == "XMLHttpRequest" {
		body = "{\"result\": \"" + result + "\"}\n"

		ctx.SetHeader("Content-Type", "application/json", true)
	} else {
		tmpl, err := template.New("uploader").Parse(uploadTemplate)

		if err != nil {
			panic(err)
		}

		var buf bytes.Buffer
		tmpl.Execute(&buf, UploadTemplateValue{result})
		body = buf.String()
	}

	if result == "ok" {
		ctx.WriteString(body)
	} else {
		ctx.Abort(500, body)
		ctx.Request.Body.Close()
	}
}

func downloader(ctx *web.Context, key string) {
	updateRemoteAddr(ctx)

	up, ok := uploadRequests[key]

	if !ok {
		ctx.NotFound("key doesn't exist")
		return
	}

	up.wait <- "connected"

	result := "ng"
	defer func() { up.wait <- result }()

	mr, err := up.request.MultipartReader()
	if err != nil {
		ctx.Abort(500, err.Error())
		return
	}

	p, err := mr.NextPart()
	if p.FormName() == "size" {
		fileSize, err := ioutil.ReadAll(p)
		if err == nil {
			fileSize, err := strconv.ParseInt(string(fileSize), 10, 64)
			if err == nil && fileSize >= 0 {
				ctx.SetHeader("Content-Length", strconv.FormatInt(fileSize, 10), true)
			}
		}
	}

	p, err = mr.NextPart()
	if err != nil {
		ctx.Abort(500, err.Error())
		return
	}
	if p.FormName() != "file" {
		ctx.Abort(500, "invalid POST (upload) request")
		return
	}

	if contentType := p.Header.Get("Content-Type"); contentType != "" {
		ctx.SetHeader("Content-Type", contentType, true)
	}

	ctx.SetHeader("Content-Disposition", "attachment; filename="+p.FileName(), true)

	_, err = io.Copy(ctx, p)

	if err == nil {
		result = "ok"
	} else {
		// XXX: may expose too many infomation (such as client IP address)
		//result = err.Error()
	}
}

func updateRemoteAddr(ctx *web.Context) {
	if appConfig.UseXForwardedFor {
		if xff := ctx.Request.Header.Get("X-Forwarded-For"); xff != "" {
			ctx.Request.RemoteAddr = xff
		}
	}
}

func main() {
	web.Get("/", indexPage)
	web.Post("/upload/(.*)", uploader)
	web.Get("/download/(.*)", downloader)

	bindHost := flag.String("bind", "0.0.0.0:8000", "bind to this address:port")
	realHost := flag.String("real-host", "", "real hostname client use to connect")
	realScheme := flag.String("real-scheme", "", "real scheme client use to connect")
	useXForwardedFor := flag.Bool("use-x-forwarded-for", false, "use X-Forwarded-For header for logging")
	logfile := flag.String("logfile", "", "log file (defaulg: stderr)")

	flag.Parse()

	appConfig = AppConfig{
		*realScheme,
		*realHost,

		"http",
		*bindHost,

		*useXForwardedFor,
	}

	if *logfile != "" {
		web.SetLogger(NewRotateLog(*logfile, 1024*1024, 10, "", log.Ldate|log.Ltime))
	}

	web.Run(appConfig.BindHost)
}
