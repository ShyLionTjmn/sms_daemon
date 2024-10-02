package main

import (
  _ "fmt"
  "log"
  "flag"
  "time"
  "errors"
  "strings"
  "sync"
  "regexp"
  "os"
  "io"
  "os/signal"
  "syscall"
  "encoding/json"
  "bytes"
  "net/http"
  . "github.com/ShyLionTjmn/m"
)

const DEFAULT_CONFIG_FILE = "/etc/sms_daemon.conf"

type Config struct {
  Peek_period             int64
  Min_file_age            int64
  Max_file_age            int64
  Sms_queue_dir           string
  Sms_host                string
  Sms_user                string
  Sms_password            string
  Sms_timeout             int64
}

var config Config
var file_reg *regexp.Regexp
var phone_reg *regexp.Regexp

func main() {
  var opt_c string
  var opt_v int

  file_reg = regexp.MustCompile(`^\d+(?:\.\d+)?$`)
  phone_reg = regexp.MustCompile(`^\+\d+$`)

  flag.StringVar(&opt_c, "c", DEFAULT_CONFIG_FILE, "Config file")
  flag.IntVar(&opt_v, "v", 0, "set verbosity level")

  flag.Parse()

  config = Config{}

  if fi, fe := os.Stat(opt_c); fe == nil && fi.Mode().IsRegular() {
    var err error
    var conf_json []byte
    if conf_json, err = os.ReadFile(opt_c); err != nil { log.Fatal(err.Error()) }

    if err = json.Unmarshal(conf_json, &config); err != nil {
      log.Fatal("Error unmarshalling config file: " + err.Error())
    }

    if config.Peek_period == 0 ||
       config.Min_file_age == 0 ||
       config.Max_file_age == 0 ||
       config.Sms_queue_dir == "" ||
       config.Sms_host == "" ||
       config.Sms_user == "" ||
       config.Sms_password == "" ||
       config.Sms_timeout == 0 ||
    false {
      log.Fatal("Insufficient config options")
    }
  } else {
    log.Fatal("Bad or missing config file " + opt_c)
  }

  for strings.HasSuffix(config.Sms_queue_dir, "/") {
    config.Sms_queue_dir = strings.TrimSuffix(config.Sms_queue_dir, "/")
  }

  sig_ch := make(chan os.Signal, 1)
  signal.Notify(sig_ch, syscall.SIGHUP)
  signal.Notify(sig_ch, syscall.SIGINT)
  signal.Notify(sig_ch, syscall.SIGTERM)
  signal.Notify(sig_ch, syscall.SIGQUIT)

  once := sync.Once{}

  log.Print("sms_daemon started")

  MAIN_LOOP:
  for {

    sleep_time := time.Duration(config.Peek_period) * time.Second
    once.Do(func() {
      sleep_time = 0
    })

    timer := time.NewTimer(sleep_time)

    select {
    case s := <-sig_ch:
      if s != syscall.SIGHUP && s != syscall.SIGUSR1 {
        timer.Stop()
        break MAIN_LOOP
      }
      continue MAIN_LOOP
    case <-timer.C:
    }

    // read queue dir

    if dir_entries, dir_err := os.ReadDir(config.Sms_queue_dir); dir_err == nil {
      for _, entry := range dir_entries {
        if !entry.IsDir() && file_reg.MatchString(entry.Name()) {
          if entry_info, info_err := entry.Info(); info_err == nil {
            if time.Now().After(
              entry_info.ModTime().Add(time.Duration(config.Max_file_age) * time.Second),
            ) {
              // remove outdated file
              if remove_err := os.Remove(config.Sms_queue_dir + "/" + entry.Name()); remove_err != nil {
                log.Println(remove_err)
              }
            } else if time.Now().After(
              entry_info.ModTime().Add(time.Duration(config.Min_file_age) * time.Second),
            ) {
              file_cont, ferr := os.ReadFile(config.Sms_queue_dir + "/" + entry.Name())
              if ferr == nil {
                remove, send_err := sendSms(string(file_cont))
                if send_err != nil {
                  log.Println(send_err)
                }

                if remove {
                  if remove_err := os.Remove(config.Sms_queue_dir + "/" + entry.Name()); remove_err != nil {
                    log.Println(remove_err)
                  }
                }
              } else {
                log.Println(ferr)
              }
            }
          } else {
            log.Println(info_err)
          }
        }
      }
    } else {
      log.Println(dir_err)
    }
  }
  log.Print("sms_daemon stopped")
}

type Message struct {
  Login        string     `json:"login"`
  Password     string     `json:"psw"`
  Phone        string     `json:"phones"`
  Message      string     `json:"mes"`
  fmt          int
}


// return true if file should be deleted
func sendSms(file_cont string) (bool, error) {
  lines := strings.Split(file_cont, "\n")

  if len(lines) < 2 {
    return true, errors.New("Not enough lines in file")
  }

  if !phone_reg.MatchString(lines[0]) {
    return true, errors.New("Bad number " + lines[0])
  }

  msg := Message {
    fmt: 3,
    Login: config.Sms_user,
    Password: config.Sms_password,
    Phone: lines[0],
    Message: strings.Join(lines[1:], "\n"),
  }

  if len(msg.Message) == 0 {
    return true, nil
  }

  js, jerr := json.MarshalIndent(msg, "", "  ")
  if jerr != nil { return true, jerr }


  log.Println("Sending to " + lines[0])

  client := &http.Client{}

  client.Timeout = time.Duration(config.Sms_timeout) * time.Second
  bodyReader := bytes.NewReader(js)

  resp, perr := client.Post(config.Sms_host, "application/json; charset=utf-8", bodyReader)

  if perr != nil {
    return false, perr
  }

  repl_body, rerr := io.ReadAll(resp.Body)

  if rerr != nil {
    return false, perr
  }

  if resp.StatusCode != 200 {
    return true, errors.New("Non 200 answer: " + resp.Status + "\n" + string(repl_body))
  }

  repl_m := M{}

  if jerr := repl_m.UnmarshalJSON(repl_body); jerr != nil {
    return true, jerr
  }

  if !repl_m.Evi("error_code") {
    log.Println("Sent successfully")
    return true, nil
  } else {
    return true, errors.New(repl_m.Vs("error"))
  }
}
