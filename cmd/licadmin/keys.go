package main

import (
	"fmt"
	"os"

	"github.com/Tencent/WeKnora/internal/license/liccmd"
)

// printKeyFingerprint 在终端打印当前私钥对应的公钥指纹与公钥文本。
//
// 为什么值得单独写一个函数：发码端最危险的失误是「用的不是同一把密钥」，
// 而这件事在终端里看不出任何异常。把指纹与可粘贴的公钥每次都打出来，
// 运维至少有一个可以逐字比对的东西。
func printKeyFingerprint(privateKeyPath string) error {
	id, err := liccmd.PrivateKeyFingerprint(privateKeyPath)
	if err != nil {
		return err
	}
	pub, err := liccmd.PublicKeyBase64Of(privateKeyPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr,
		"私钥：%s\n公钥指纹（KeyID）：%s\n公钥（base64，写入客户端 config/license/public.key）：\n%s\n\n",
		privateKeyPath, id, pub)
	return nil
}
