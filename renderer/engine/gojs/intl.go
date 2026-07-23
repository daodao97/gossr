package gojs

import (
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dop251/goja"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// goja 不提供 Intl。缺了它，任何在渲染期碰 Intl 的依赖（reka-ui 经
// @internationalized/date 推断时区、chart.js 格式化刻度等）都会抛错，
// 而 Vue 会吞掉这个错误、把整棵子树渲染成空，最终在浏览器里表现为
// "Hydration node mismatch: rendered on server <!----> expected on client div"。
//
// 这里只补两件事，且时区换算交给 Go 的 tzdata，而不是在 JS 里估算：
//   - Intl.DateTimeFormat：resolvedOptions().timeZone 与按时区拆分的 formatToParts
//   - Intl.NumberFormat：分组与小数位
//
// 时区取自 TZ 环境变量（未设置则 UTC）。这与 goja 的 Date 共用同一来源
// （goja 用 time.Local，同样由 TZ 决定），因此 @internationalized/date 的
// getTimezoneOffset 快路径和这里的 formatToParts 不会给出互相矛盾的结果。
//
// 注意：垫片保证的是"服务端能渲染出来"，不是"和浏览器逐字节相同"。
// 浏览器用的是访问者本地时区，服务端用的是 TZ。凡是渲染结果依赖时区的
// 组件，仍应固定时区或改为挂载后渲染，不能指望这个垫片对齐。
//
// 关于该补哪些成员，有一条反直觉但重要的规则：
//
//	只补库「无条件调用」的成员；库自己做特性检测的，宁可不补。
//
// 例如 @internationalized/date 是这样用 Intl.Locale 的：
//
//	if (Intl.Locale) { region = new Intl.Locale(tag).maximize().region }
//	else             { region = tag.split('-')[1] }
//
// 它自带回退。此时提供一个 maximize() 不准的 Intl.Locale，等于骗过特性检测、
// 让库拿到错误的地区和每周首日 —— 比不提供更糟。反过来 reka-ui 的 useFilter
// 直接 new Intl.Collator(...)，没有回退，不补就会抛错丢子树，所以必须补。
//
// 结论：不要为了"看起来完整"而塞存根。加一个成员前先确认库是无条件调用它的。
func installIntlPolyfill(rt *goja.Runtime, global *goja.Object) {
	zoneName, location := ssrTimeZone()

	_ = global.Set("__gossrIntlTimeZone", func(goja.FunctionCall) goja.Value {
		return rt.ToValue(zoneName)
	})

	_ = global.Set("__gossrIntlParts", func(call goja.FunctionCall) goja.Value {
		epochMillis := call.Argument(0).ToFloat()
		target := location
		if requested := strings.TrimSpace(call.Argument(1).String()); requested != "" && requested != zoneName {
			if loaded, err := time.LoadLocation(requested); err == nil {
				target = loaded
			}
		}

		moment := time.UnixMilli(int64(epochMillis)).In(target)
		year, era := moment.Year(), "AD"
		if year <= 0 {
			// ISO 年 0 是公元前 1 年；@internationalized/date 用 era 判断是否取负。
			year, era = 1-year, "BC"
		}

		parts := []map[string]string{
			{"type": "era", "value": era},
			{"type": "year", "value": strconv.Itoa(year)},
			{"type": "month", "value": strconv.Itoa(int(moment.Month()))},
			{"type": "day", "value": strconv.Itoa(moment.Day())},
			{"type": "hour", "value": strconv.Itoa(moment.Hour())},
			{"type": "minute", "value": strconv.Itoa(moment.Minute())},
			{"type": "second", "value": strconv.Itoa(moment.Second())},
		}
		return rt.ToValue(parts)
	})

	// reka-ui 的 useFilter 会无条件 new Intl.Collator(...)，没有回退分支。
	// 折叠交给 x/text，避免只处理 ASCII 大小写而对重音字符给出错误结果。
	_ = global.Set("__gossrIntlFold", func(call goja.FunctionCall) goja.Value {
		text := call.Argument(0).String()
		switch strings.ToLower(strings.TrimSpace(call.Argument(1).String())) {
		case "base":
			return rt.ToValue(stripDiacritics(strings.ToLower(text)))
		case "accent":
			return rt.ToValue(strings.ToLower(text))
		case "case":
			return rt.ToValue(stripDiacritics(text))
		default:
			return rt.ToValue(text)
		}
	})

	if _, err := rt.RunString(intlPolyfillScript); err != nil {
		panic("failed to install Intl polyfill: " + err.Error())
	}

	_ = global.Delete("__gossrIntlTimeZone")
	_ = global.Delete("__gossrIntlParts")
	_ = global.Delete("__gossrIntlFold")
}

// stripDiacritics 去掉组合用变音符号：café -> cafe。
func stripDiacritics(text string) string {
	folded, _, err := transform.String(
		transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC),
		text,
	)
	if err != nil {
		return text
	}
	return folded
}

// ssrTimeZone 返回 SSR 使用的时区名与对应 Location。
// 与 goja 的 Date 保持同源：两者都由 TZ 决定。
func ssrTimeZone() (string, *time.Location) {
	name := strings.TrimSpace(os.Getenv("TZ"))
	if name == "" {
		return "UTC", time.UTC
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return "UTC", time.UTC
	}
	return name, location
}

const intlPolyfillScript = `
(function (global) {
  if (global.Intl) return

  var defaultTimeZone = __gossrIntlTimeZone()
  var timeZoneParts = __gossrIntlParts
  var fold = __gossrIntlFold

  function toEpochMillis(value) {
    if (value === undefined) return Date.now()
    if (value instanceof Date) return value.getTime()
    return Number(value)
  }

  function DateTimeFormat(locale, options) {
    if (!(this instanceof DateTimeFormat)) return new DateTimeFormat(locale, options)
    this._locale = (Array.isArray(locale) ? locale[0] : locale) || 'en-US'
    this._options = options || {}
    this._timeZone = this._options.timeZone || defaultTimeZone
  }

  DateTimeFormat.prototype.resolvedOptions = function () {
    var resolved = { locale: this._locale, timeZone: this._timeZone,
                     calendar: 'gregory', numberingSystem: 'latn' }
    for (var key in this._options) {
      if (Object.prototype.hasOwnProperty.call(this._options, key) && key !== 'timeZone')
        resolved[key] = this._options[key]
    }
    return resolved
  }

  DateTimeFormat.prototype.formatToParts = function (value) {
    return timeZoneParts(toEpochMillis(value), this._timeZone)
  }

  DateTimeFormat.prototype.format = function (value) {
    var found = {}
    this.formatToParts(value).forEach(function (part) { found[part.type] = part.value })
    function pad(text) { return text.length < 2 ? '0' + text : text }
    return found.year + '-' + pad(found.month) + '-' + pad(found.day) + ' ' +
           pad(found.hour) + ':' + pad(found.minute) + ':' + pad(found.second)
  }

  function NumberFormat(locale, options) {
    if (!(this instanceof NumberFormat)) return new NumberFormat(locale, options)
    this._locale = (Array.isArray(locale) ? locale[0] : locale) || 'en-US'
    this._options = options || {}
  }

  NumberFormat.prototype.resolvedOptions = function () {
    return { locale: this._locale, numberingSystem: 'latn', style: this._options.style || 'decimal' }
  }

  NumberFormat.prototype.format = function (value) {
    var amount = Number(value)
    if (!isFinite(amount)) return String(amount)

    var minimum = this._options.minimumFractionDigits
    var maximum = this._options.maximumFractionDigits
    if (minimum === undefined) minimum = 0
    if (maximum === undefined) maximum = Math.max(minimum, 3)

    var text = amount.toFixed(maximum)
    if (maximum > minimum) {
      text = text.replace(/0+$/, '')
      var decimals = text.indexOf('.') === -1 ? 0 : text.length - text.indexOf('.') - 1
      if (decimals < minimum) text = amount.toFixed(minimum)
      else text = text.replace(/\.$/, '')
    }

    if (this._options.useGrouping === false) return text
    var pieces = text.split('.')
    pieces[0] = pieces[0].replace(/\B(?=(\d{3})+(?!\d))/g, ',')
    return pieces.join('.')
  }

  NumberFormat.prototype.formatToParts = function (value) {
    return [{ type: 'literal', value: this.format(value) }]
  }

  function Collator(locale, options) {
    if (!(this instanceof Collator)) return new Collator(locale, options)
    this._locale = (Array.isArray(locale) ? locale[0] : locale) || 'en'
    this._options = options || {}
    this._sensitivity = this._options.sensitivity || 'variant'
  }

  Collator.prototype.resolvedOptions = function () {
    return { locale: this._locale, usage: this._options.usage || 'sort',
             sensitivity: this._sensitivity, numeric: !!this._options.numeric,
             collation: 'default', ignorePunctuation: !!this._options.ignorePunctuation }
  }

  Collator.prototype.compare = function (left, right) {
    var a = fold(String(left), this._sensitivity)
    var b = fold(String(right), this._sensitivity)
    if (this._options.numeric) {
      var na = parseFloat(a), nb = parseFloat(b)
      if (!isNaN(na) && !isNaN(nb) && na !== nb) return na < nb ? -1 : 1
    }
    return a === b ? 0 : (a < b ? -1 : 1)
  }

  global.Intl = { DateTimeFormat: DateTimeFormat, NumberFormat: NumberFormat, Collator: Collator }
})(globalThis)
`
