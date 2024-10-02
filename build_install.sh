#!/bin/sh
go build && sudo install sms_daemon /usr/local/sbin/ && sudo systemctl restart sms_daemon && sleep 1 && sudo systemctl --no-pager status sms_daemon && echo "Check later:" && echo "sudo systemctl --no-pager status sms_daemon"
