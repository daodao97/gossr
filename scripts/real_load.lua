local paths = {
  "/",
  "/zh",
  "/hi/gopher",
  "/hi/vue?title=Ms.",
  "/seo-demo?title=Load%20Test",
  "/session-demo",
  "/no-ssr-fetch",
  "/404",
}

local index = 0
local unexpected_status = 0

request = function()
  index = (index % #paths) + 1
  return wrk.format("GET", paths[index])
end

response = function(status)
  if status ~= 200 then
    unexpected_status = unexpected_status + 1
  end
end

done = function()
  io.write(string.format("unexpected_status=%d\n", unexpected_status))
end
