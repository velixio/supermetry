export function total(items) {
  let sum = 0;
  for (const item of items) {
    sum += item.price * item.qty;
  }
  return sum;
}

export function checkout(items) {
  const amount = total(items);
  if (amount > 100) {
    throw new Error("cart limit exceeded");
  }
  return amount;
}
