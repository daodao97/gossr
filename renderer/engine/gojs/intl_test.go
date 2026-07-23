package gojs

import (
	"testing"

	"github.com/dop251/goja"
)

func newIntlRuntime(t *testing.T) *goja.Runtime {
	t.Helper()
	rt := goja.New()
	global := rt.GlobalObject()
	_ = global.Set("globalThis", global)
	installIntlPolyfill(rt, global)
	return rt
}

func evalString(t *testing.T, rt *goja.Runtime, script string) string {
	t.Helper()
	value, err := rt.RunString(script)
	if err != nil {
		t.Fatalf("eval %q: %v", script, err)
	}
	return value.String()
}

// formatToParts 必须真的按请求的时区换算，而不是一律返回 UTC。
// 这是 @internationalized/date 推算时区偏移的唯一依据，答错会让日期整体偏一天。
func TestIntlFormatToPartsUsesRequestedTimeZone(t *testing.T) {
	rt := newIntlRuntime(t)

	// 2026-07-18T09:40:02Z 在东八区是当日 17:40，在檀香山是当日 23:40 的前一天。
	const script = `
		function partsOf(zone) {
			var found = {}
			new Intl.DateTimeFormat('en-US', { timeZone: zone })
				.formatToParts(new Date(Date.UTC(2026, 6, 18, 9, 40, 2)))
				.forEach(function (part) { found[part.type] = part.value })
			return found.year + '/' + found.month + '/' + found.day + ' ' + found.hour + ':' + found.minute
		}
	`
	if _, err := rt.RunString(script); err != nil {
		t.Fatalf("define helper: %v", err)
	}

	for _, testCase := range []struct{ zone, want string }{
		{"UTC", "2026/7/18 9:40"},
		{"Asia/Shanghai", "2026/7/18 17:40"},
		{"Pacific/Honolulu", "2026/7/17 23:40"},
		{"America/New_York", "2026/7/18 5:40"},
	} {
		if got := evalString(t, rt, `partsOf(`+"`"+testCase.zone+"`"+`)`); got != testCase.want {
			t.Fatalf("%s = %q, want %q", testCase.zone, got, testCase.want)
		}
	}
}

// 无参构造必须报出一个可用的时区名，getLocalTimeZone() 依赖它。
func TestIntlResolvedOptionsReportsTimeZone(t *testing.T) {
	rt := newIntlRuntime(t)
	if got := evalString(t, rt, `new Intl.DateTimeFormat().resolvedOptions().timeZone`); got == "" {
		t.Fatal("resolvedOptions().timeZone is empty")
	}
	if got := evalString(t, rt, `new Intl.DateTimeFormat('en-US', {timeZone: 'Asia/Tokyo'}).resolvedOptions().timeZone`); got != "Asia/Tokyo" {
		t.Fatalf("timeZone = %q, want Asia/Tokyo", got)
	}
}

// 不带 new 调用在真实 Intl 里是合法的，某些库会这么用。
func TestIntlConstructorsWorkWithoutNew(t *testing.T) {
	rt := newIntlRuntime(t)
	if got := evalString(t, rt, `Intl.NumberFormat('en-US').format(1234567.5)`); got != "1,234,567.5" {
		t.Fatalf("NumberFormat without new = %q", got)
	}
	if got := evalString(t, rt, `typeof Intl.DateTimeFormat('en-US').resolvedOptions().timeZone`); got != "string" {
		t.Fatalf("DateTimeFormat without new = %q", got)
	}
}

func TestIntlNumberFormatOptions(t *testing.T) {
	rt := newIntlRuntime(t)
	for _, testCase := range []struct{ script, want string }{
		{`Intl.NumberFormat('zh-CN').format(1234567)`, "1,234,567"},
		{`Intl.NumberFormat('zh-CN', {useGrouping: false}).format(1234567)`, "1234567"},
		{`Intl.NumberFormat('en-US', {maximumFractionDigits: 1}).format(12.34)`, "12.3"},
		{`Intl.NumberFormat('en-US', {minimumFractionDigits: 2}).format(12)`, "12.00"},
		{`Intl.NumberFormat('en-US', {minimumFractionDigits: 0, maximumFractionDigits: 4}).format(1234.5)`, "1,234.5"},
	} {
		if got := evalString(t, rt, testCase.script); got != testCase.want {
			t.Fatalf("%s = %q, want %q", testCase.script, got, testCase.want)
		}
	}
}

// 宿主已经提供 Intl 时不覆盖，同时临时的 host function 不应泄漏到全局。
func TestIntlPolyfillDoesNotLeakHelpers(t *testing.T) {
	rt := newIntlRuntime(t)
	for _, helper := range []string{"__gossrIntlTimeZone", "__gossrIntlParts"} {
		if got := evalString(t, rt, `typeof `+helper); got != "undefined" {
			t.Fatalf("%s leaked into globals: %s", helper, got)
		}
	}
}

// reka-ui 的 useFilter 无条件构造 Collator，不补就抛错丢子树。
func TestIntlCollatorCompare(t *testing.T) {
	rt := newIntlRuntime(t)
	for _, testCase := range []struct{ script, want string }{
		{`String(Intl.Collator('en').compare('a', 'b'))`, "-1"},
		{`String(Intl.Collator('en').compare('b', 'a'))`, "1"},
		{`String(Intl.Collator('en').compare('a', 'a'))`, "0"},
		// sensitivity: base 应同时忽略大小写与变音符号
		{`String(new Intl.Collator('en', {sensitivity: 'base'}).compare('café', 'CAFE'))`, "0"},
		{`String(new Intl.Collator('en', {sensitivity: 'accent'}).compare('cafe', 'CAFE'))`, "0"},
		{`String(new Intl.Collator('en', {sensitivity: 'accent'}).compare('café', 'cafe'))`, "1"},
		{`String(new Intl.Collator('en', {numeric: true}).compare('10', '9'))`, "1"},
		{`new Intl.Collator('en', {usage: 'search'}).resolvedOptions().usage`, "search"},
	} {
		if got := evalString(t, rt, testCase.script); got != testCase.want {
			t.Fatalf("%s = %q, want %q", testCase.script, got, testCase.want)
		}
	}
}

// 库自带回退的成员不该被存根骗过特性检测。
func TestIntlOmitsFeatureDetectedMembers(t *testing.T) {
	rt := newIntlRuntime(t)
	for _, member := range []string{"Locale", "PluralRules", "RelativeTimeFormat", "ListFormat"} {
		if got := evalString(t, rt, `typeof Intl.`+member); got != "undefined" {
			t.Fatalf("Intl.%s 被存根了 (%s)；库会因此跳过自带回退并拿到错误结果", member, got)
		}
	}
}
