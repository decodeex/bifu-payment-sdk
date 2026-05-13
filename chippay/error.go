package chippay

import "errors"

var (
	ErrInvalidMerchantOrderID = errors.New("invalid merchant order ID")
	ErrInvalidAmount          = errors.New("invalid amount")
	ErrInvalidCurrency        = errors.New("invalid currency")
	ErrInvalidCustomerPhone   = errors.New("invalid customer phone")
	ErrInvalidCustomerName    = errors.New("invalid customer name")
	ErrInvalidName            = errors.New("invalid name")
)
