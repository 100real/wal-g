package mongoload

import (
	"bufio"
	"encoding/json"
	"fmt"
	testUtil "github.com/wal-g/wal-g/tests_func/utils"
	"math/rand"
	"os"
	"strings"
	"time"
)

type opConfig struct {
	Op      string            `json:"op"`
	Cnt     int               `json:"cnt"`
	DbName  string            `json:"db"`
	ColName string            `json:"col"`
	Cmds    []json.RawMessage `json:"cmds"`
	Adv     json.RawMessage   `json:"adv"`
}

type advInsert struct {
	Values    []string `json:"values"`
	MnValLen  int      `json:"mn_val_len"`
	MxValLen  int      `json:"mx_val_len"`
	Keys      []string `json:"keys"`
	MnKeyLen  int      `json:"mn_key_len"`
	MxKeyLen  int      `json:"mx_key_len"`
	MnDocsCnt int      `json:"mn_docs_cnt"`
	MxDocsCnt int      `json:"mx_docs_cnt"`
	MnKeysCnt int      `json:"mn_keys_cnt"`
	MxKeysCnt int      `json:"mx_keys_cnt"`
}

type patronConfig struct {
	PatronName string     `json:"name"`
	OpConfig   []opConfig `json:"config"`
}

var id int

func updateMnMx(inMn, inMx, eMn, eMx int) (int, int) {
	if inMn == 0 {
		inMn = eMn
	}
	if inMx == 0 {
		inMx = eMx
	}
	if inMx < inMn {
		inMx = inMn
	}
	return inMn, inMx
}

func valueGenF(values []string, mnLen, mxLen int) func() string {
	if len(values) == 0 {
		return func() string {
			mnLen, mxLen = updateMnMx(mnLen, mxLen, 2, 10)
			ln := rand.Intn(mxLen-mnLen+1) + mnLen
			return testUtil.RandSeq(ln)
		}
	}
	return func() string {
		idx := rand.Intn(len(values))
		return values[idx]
	}
}

var processOp = map[string]func(config opConfig) (string, error){
	"insert": func(config opConfig) (string, error) {
		var adv advInsert
		if len(config.Adv) != 0 {
			err := json.Unmarshal(config.Adv, &adv)
			if err != nil {
				fmt.Println(err)
				return "", err
			}
		}
		valueGen := valueGenF(adv.Values, adv.MnValLen, adv.MxValLen)
		keyGen := valueGenF(adv.Keys, adv.MnKeyLen, adv.MxKeyLen)
		docsGen := func() string {
			adv.MnDocsCnt, adv.MxDocsCnt = updateMnMx(adv.MnDocsCnt, adv.MxDocsCnt, 1, 3)
			adv.MnKeysCnt, adv.MxKeysCnt = updateMnMx(adv.MnKeysCnt, adv.MxKeysCnt, 1, 3)
			dCnt := rand.Intn(adv.MxDocsCnt-adv.MnDocsCnt+1) + adv.MnDocsCnt
			kCnt := rand.Intn(adv.MxKeysCnt-adv.MnKeysCnt+1) + adv.MnDocsCnt
			docs := "["
			for d := 0; d < dCnt; d++ {
				doc := "{"
				for k := 0; k < kCnt; k++ {
					doc += fmt.Sprintf(`"%s": "%s"`, keyGen(), valueGen())
					if k != kCnt-1 {
						doc += ", "
					}
				}
				doc += "}"
				if d != dCnt-1 {
					doc += ", "
				}
				docs += doc
			}
			docs += "]"
			return docs
		}

		id++
		return fmt.Sprintf(`{"op":"c", "db":"%s", "id": %d, "dc":{"insert":"%s", "documents": %s}}`,
			config.DbName, id, config.ColName, docsGen()), nil

	},
}

func addIndeciesAfterOp(str string) string {
	i := strings.Index(str, `"op"`)
	if i == -1 {
		return str
	}
	id++
	return str[:i] + fmt.Sprintf(`"id": %d, "op"`, id) + addIndeciesAfterOp(str[(i+4):])
}

func generateOp(writer *bufio.Writer, config opConfig, lastComma bool) error {
	if config.Cmds != nil {
		if config.Op != "" {
			return fmt.Errorf("if explicit cmds is used, op field cannot be set")
		}
		var res []string
		for _, cmd := range config.Cmds {
			fstr := strings.Map(func(r rune) rune {
				if strings.Contains(" \n\t\r", string(r)) {
					return -1
				}
				return r
			}, string(cmd))
			fstr = addIndeciesAfterOp(fstr)
			res = append(res, fstr)
		}
		cmdLine := strings.Join(res, ",\n")
		if lastComma {
			cmdLine += ","
		}
		cmdLine += "\n"
		_, err := writer.WriteString(cmdLine)
		if err != nil {
			return fmt.Errorf("cannot generate op %s: %v", config.Op, err)
		}
		return nil
	}
	for i := 0; i < config.Cnt; i++ {
		cmdLine, err := processOp[config.Op](config)
		if err != nil {
			return fmt.Errorf("cannot generate op %s: %v", config.Op, err)
		}
		if i != config.Cnt-1 || lastComma {
			cmdLine = cmdLine + ","
		}
		cmdLine = cmdLine + "\n"
		_, err = writer.WriteString(cmdLine)
		if err != nil {
			return fmt.Errorf("cannot generate op %s: %v", config.Op, err)
		}
	}
	return nil
}

func generatePatron(config patronConfig) error {

	//hndl, err := os.Create("tests_func/load/" + config.PatronName + ".json")
	hndl, err := os.Create(config.PatronName + ".json")
	if err != nil {
		return fmt.Errorf("cannot generate patron %s: %v", config.PatronName, err)
	}
	defer hndl.Close()
	w := bufio.NewWriter(hndl)

	_, err = w.WriteString("[\n")
	for i, opConfig := range config.OpConfig {
		err = generateOp(w, opConfig, i != len(config.OpConfig)-1)
		if err != nil {
			return fmt.Errorf("cannot generate patron %s: %v", config.PatronName, err)
		}
	}
	_, err = w.WriteString("]\n")

	err = w.Flush()
	if err != nil {
		return fmt.Errorf("cannot generate patron %s: %v", config.PatronName, err)
	}

	return nil
}

func generatePatronsFromFile(filepath string) error {
	rand.Seed(time.Now().UnixNano())
	hndl, err := os.Open(filepath)
	if err != nil {
		return fmt.Errorf("cannot generate patrons from file: %v", err)
	}
	defer hndl.Close()
	decoder := json.NewDecoder(hndl)
	var configs []patronConfig
	err = decoder.Decode(&configs)
	if err != nil {
		return fmt.Errorf("cannot decode config JSON: %v", err)
	}
	for i := range configs {
		id = 0
		err = generatePatron(configs[i])
		if err != nil {
			return err
		}
	}
	return nil
}
