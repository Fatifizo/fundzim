/**
 * /admin/payments/transactions (Stage 9 route name): the provider transaction list is the payment attempts list —
 * one row per payment with its provider status. Same page, same permission (`payment.view`).
 */
export { default, metadata } from "../attempts/page";
