# Searching is free and buys nothing.
data "livekit_available_phone_numbers" "sf" {
  country_code = "US"
  area_code    = "415"
  limit        = 5
}

output "for_sale" {
  value = [for n in data.livekit_available_phone_numbers.sf.numbers : n.phone_number]
}
